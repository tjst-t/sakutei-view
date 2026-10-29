package aggregate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tjst-t/sakutei-view/internal/source/containerlab"
	"github.com/tjst-t/sakutei-view/internal/source/plugin"
	"github.com/tjst-t/sakutei-view/internal/source/sakutei"
)

// mockContainerlabSource is a fixed-topology containerlab.Source, standing
// in for either real backend in tests.
type mockContainerlabSource struct {
	topo containerlab.Topology
}

func (m mockContainerlabSource) Topology(ctx context.Context) (containerlab.Topology, error) {
	return m.topo, nil
}

const sakuteiStatusFixture = `{
  "schema_version": 1,
  "generated_at": "2026-10-28T00:00:00Z",
  "complete": true,
  "cycle": 10,
  "cycle_time": "2026-10-28T00:00:00Z",
  "last_cycle": 10,
  "summary": {
    "conformant": 1, "drift": 0, "missing": 0, "unobservable": 0,
    "unmanaged": 0, "workflow": 0, "oscillating": 1, "list_errors": 0, "total": 2
  },
  "resources": [
    {"id": "res-leaf1", "provider": "containerlab", "scope": {}, "local_id": "leaf1", "state": "conformant"},
    {"id": "res-spine1", "provider": "containerlab", "scope": {}, "local_id": "spine1", "state": "oscillating",
     "apply_stopped": {"reason": "max retries exceeded"}}
  ]
}`

const pluginStatusFixture = `{
  "generated_at": "2026-10-28T00:00:00Z",
  "resources": [
    {"id": "res-node3", "kind": "iface", "label": "node3", "state": "drifted", "node": "node3",
     "fields": {}, "drifted_fields": ["mtu"]}
  ]
}`

const sakuteiPlanFixture = `{
  "schema_version": 1,
  "generated_at": "2026-10-28T00:00:00Z",
  "summary": {"changes": 0, "create": 0, "update": 0, "delete": 0, "destructive": 0, "blocked": 0,
    "planned_change_lane": 0, "task": 0, "workflow": 0, "pending_allocations": 0, "unobservable": 0, "prune_halts": 0},
  "operations": [],
  "budget": {"halted": true, "halt": "destructive", "reason": "destructive budget exhausted"},
  "prune_halts": [
    {"provider": "containerlab", "reason": "count drop safety", "count_drop": true}
  ]
}`

// The nodes' local_id ("leaf1"/"spine1") is deliberately different from
// their full id ("res-leaf1"/"res-spine1") here, and "waves"/"edges"/
// "waiting_for" below use local_id -- matching Sakutei's actual wire format
// (docs/read-api.md: "waves" lists each wave's members by local_id, not
// by the full id). A fixture where id and local_id happened to coincide
// would not have caught the local_id/id mismatch bug this guards against.
const sakuteiGraphFixture = `{
  "schema_version": 1,
  "generated_at": "2026-10-28T00:00:00Z",
  "nodes": [
    {"id": "res-leaf1", "provider": "containerlab", "scope": {}, "local_id": "leaf1",
     "depends_on": [], "observed": true, "ready": true, "wave": 0},
    {"id": "res-spine1", "provider": "containerlab", "scope": {}, "local_id": "spine1",
     "depends_on": ["leaf1"], "observed": true, "ready": false, "waiting_for": "leaf1", "wave": 1}
  ],
  "edges": [
    {"from": "leaf1", "to": "spine1"}
  ],
  "waves": [["leaf1"], ["spine1"]],
  "summary": {"nodes": 2, "external": 0, "edges": 1, "waves": 2, "not_ready": 1}
}`

func newTestAggregator(t *testing.T) *Aggregator {
	t.Helper()

	sakuteiMux := http.NewServeMux()
	sakuteiMux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sakuteiStatusFixture))
	})
	sakuteiMux.HandleFunc("GET /plan", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sakuteiPlanFixture))
	})
	sakuteiMux.HandleFunc("GET /graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sakuteiGraphFixture))
	})
	sakuteiSrv := httptest.NewServer(sakuteiMux)
	t.Cleanup(sakuteiSrv.Close)

	pluginMux := http.NewServeMux()
	pluginMux.HandleFunc("GET /plugin/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(pluginStatusFixture))
	})
	pluginSrv := httptest.NewServer(pluginMux)
	t.Cleanup(pluginSrv.Close)

	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{
			{Name: "leaf1", State: "running"},
			{Name: "spine1", State: "running"},
			{Name: "node3", State: "running"},
			{Name: "down-node", State: "exited", Status: "Exited (1) 3 minutes ago"},
		},
		Links: []containerlab.Link{
			{Type: "veth", A: containerlab.LinkEndpoint{Node: "leaf1"}, B: containerlab.LinkEndpoint{Node: "spine1"}},
		},
	}}

	return New(cl, sakutei.NewClient(sakuteiSrv.URL, nil), plugin.NewClient(pluginSrv.URL, nil))
}

func TestAggregator_Snapshot(t *testing.T) {
	agg := newTestAggregator(t)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if len(snap.Nodes) != 4 {
		t.Fatalf("Nodes = %d, want 4", len(snap.Nodes))
	}
	if len(snap.Links) != 1 {
		t.Fatalf("Links = %d, want 1", len(snap.Links))
	}
	if snap.SakuteiSummary == nil || snap.SakuteiSummary.Total != 2 {
		t.Fatalf("SakuteiSummary = %+v, want Total=2", snap.SakuteiSummary)
	}

	if len(snap.SakuteiStopReasons) != 3 {
		t.Fatalf("SakuteiStopReasons = %+v, want 3 entries", snap.SakuteiStopReasons)
	}
	wantReasons := map[string]StopReason{
		"resource:spine1":    {Scope: "resource", Target: "spine1", Reason: "max retries exceeded"},
		"budget:":            {Scope: "budget", Reason: "destructive budget exhausted"},
		"prune:containerlab": {Scope: "prune", Target: "containerlab", Reason: "count drop safety"},
	}
	for _, got := range snap.SakuteiStopReasons {
		key := got.Scope + ":" + got.Target
		want, ok := wantReasons[key]
		if !ok || got != want {
			t.Errorf("unexpected StopReason %+v (want one of %+v)", got, wantReasons)
		}
	}

	if snap.SakuteiGraph == nil || len(snap.SakuteiGraph.Waves) != 2 {
		t.Fatalf("SakuteiGraph = %+v, want 2 waves", snap.SakuteiGraph)
	}
	if w0 := snap.SakuteiGraph.Waves[0]; len(w0) != 1 || w0[0].ID != "res-leaf1" || !w0[0].Ready {
		t.Errorf("Waves[0] = %+v, want one ready node res-leaf1", w0)
	}
	if w1 := snap.SakuteiGraph.Waves[1]; len(w1) != 1 || w1[0].ID != "res-spine1" || w1[0].Ready || w1[0].WaitingFor != "leaf1" {
		t.Errorf("Waves[1] = %+v, want one not-ready node res-spine1 waiting_for leaf1", w1)
	}

	byName := map[string]Node{}
	for _, n := range snap.Nodes {
		byName[n.Name] = n
	}

	tests := []struct {
		name string
		want Color
	}{
		{"leaf1", ColorOK},
		{"spine1", ColorApplyFailed},
		{"node3", ColorDrift},
		{"down-node", ColorDown},
	}
	for _, tt := range tests {
		n, ok := byName[tt.name]
		if !ok {
			t.Fatalf("node %q missing from snapshot", tt.name)
		}
		if got := n.Color(); got != tt.want {
			t.Errorf("Color(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}

	leaf1 := byName["leaf1"]
	if leaf1.Sakutei == nil || leaf1.Sakutei.State != "conformant" {
		t.Errorf("leaf1.Sakutei = %+v, want State=conformant", leaf1.Sakutei)
	}
	spine1 := byName["spine1"]
	if spine1.Sakutei == nil || !spine1.Sakutei.ApplyFailed {
		t.Errorf("spine1.Sakutei = %+v, want ApplyFailed=true", spine1.Sakutei)
	}
	node3 := byName["node3"]
	if node3.Plugin == nil || node3.Plugin.State != "drifted" || len(node3.Plugin.DriftedFields) != 1 {
		t.Errorf("node3.Plugin = %+v, unexpected", node3.Plugin)
	}
	downNode := byName["down-node"]
	if downNode.Sakutei != nil || downNode.Plugin != nil {
		t.Errorf("down-node has an overlay, want none: %+v", downNode)
	}
}

// TestAggregator_Snapshot_LinksNeverNull verifies that Snapshot.Links is
// always a JSON array ([]), never null, even when containerlab has never
// been polled successfully (a zero-value containerlab.Topology has a nil
// Links slice). Frontend code treats Nodes/Links as always-arrays; a null
// here previously crashed the topology diagram.
func TestAggregator_Snapshot_LinksNeverNull(t *testing.T) {
	cl := mockContainerlabSource{topo: containerlab.Topology{}} // zero value: Nodes and Links both nil
	agg := New(cl, nil, nil)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Links == nil {
		t.Fatalf("Links = nil, want a non-nil (possibly empty) slice so it serializes as [] not null")
	}
	if len(snap.Links) != 0 {
		t.Errorf("Links = %+v, want empty", snap.Links)
	}

	b, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if string(raw["links"]) != "[]" {
		t.Errorf(`links JSON = %s, want "[]"`, raw["links"])
	}
}

// TestAggregator_Snapshot_SakuteiSourcesAndPolicySources verifies that a
// plugin's optional sakutei_sources/policy_sources (contract v0.4) are
// passed through into the Snapshot as-is, and that both are simply absent
// (nil, not empty-non-nil) when the plugin's response has neither field --
// matching a pre-v0.4 plugin's responses.
func TestAggregator_Snapshot_SakuteiSourcesAndPolicySources(t *testing.T) {
	pluginMux := http.NewServeMux()
	pluginMux.HandleFunc("GET /plugin/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"generated_at": "2026-10-28T00:00:00Z",
			"resources": [],
			"sakutei_sources": [
				{"resource_id": "res-1", "file": "network.yml", "key": "resources[name=res-1]", "why": "reachability"}
			],
			"policy_sources": [
				{"name": "rule-1", "from": "a0", "to": "fw1", "proto": "tcp", "ports": [22], "file": "network.yml", "key": "firewall.rules[name=rule-1]", "why": "ssh mgmt"}
			]
		}`))
	})
	pluginSrv := httptest.NewServer(pluginMux)
	defer pluginSrv.Close()

	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{{Name: "solo", State: "running"}},
	}}
	agg := New(cl, nil, plugin.NewClient(pluginSrv.URL, nil))

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.SakuteiSources) != 1 || snap.SakuteiSources[0].ResourceID != "res-1" {
		t.Fatalf("SakuteiSources = %+v, want one entry with ResourceID=res-1", snap.SakuteiSources)
	}
	if snap.SakuteiSources[0].File == nil || *snap.SakuteiSources[0].File != "network.yml" {
		t.Errorf("SakuteiSources[0].File = %v, want \"network.yml\"", snap.SakuteiSources[0].File)
	}
	if len(snap.PolicySources) != 1 || snap.PolicySources[0].Name != "rule-1" {
		t.Fatalf("PolicySources = %+v, want one entry named rule-1", snap.PolicySources)
	}

	// Absent entirely, as a pre-v0.4 plugin would send.
	pluginMuxOld := http.NewServeMux()
	pluginMuxOld.HandleFunc("GET /plugin/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"generated_at": "2026-10-28T00:00:00Z", "resources": []}`))
	})
	pluginSrvOld := httptest.NewServer(pluginMuxOld)
	defer pluginSrvOld.Close()
	aggOld := New(cl, nil, plugin.NewClient(pluginSrvOld.URL, nil))
	snapOld, err := aggOld.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot (old plugin): %v", err)
	}
	if snapOld.SakuteiSources != nil {
		t.Errorf("SakuteiSources = %+v, want nil for a pre-v0.4 plugin response", snapOld.SakuteiSources)
	}
	if snapOld.PolicySources != nil {
		t.Errorf("PolicySources = %+v, want nil for a pre-v0.4 plugin response", snapOld.PolicySources)
	}
}

func TestAggregator_Snapshot_NoOptionalSources(t *testing.T) {
	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{{Name: "solo", State: "running"}},
	}}
	agg := New(cl, nil, nil)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].Color() != ColorUnknown {
		t.Errorf("Nodes = %+v, want one node colored unknown", snap.Nodes)
	}
	if snap.SakuteiSummary != nil {
		t.Errorf("SakuteiSummary = %+v, want nil", snap.SakuteiSummary)
	}
}

// TestAggregator_Snapshot_RequestTimeout verifies that a source which never
// responds cannot block Snapshot forever: with a short RequestTimeout,
// Snapshot must return promptly, with the other (healthy) source's data
// intact and the hung source's failure recorded in Health.
func TestAggregator_Snapshot_RequestTimeout(t *testing.T) {
	block := make(chan struct{})
	sakuteiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond
	}))
	defer func() {
		close(block) // let the hung handler return before the server closes
		sakuteiSrv.Close()
	}()

	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{{Name: "n1", State: "running"}},
	}}
	agg := New(cl, sakutei.NewClient(sakuteiSrv.URL, nil), nil)
	agg.RequestTimeout = 50 * time.Millisecond

	start := time.Now()
	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Snapshot took %s, want it to return soon after the per-request timeout fires", elapsed)
	}
	if len(snap.Nodes) != 1 {
		t.Fatalf("Nodes = %d, want 1 (containerlab still worked despite sakutei hanging)", len(snap.Nodes))
	}
	h := snap.Health[SourceSakutei]
	if !h.Configured || h.OK || h.LastError == "" {
		t.Errorf("Sakutei health = %+v, want Configured=true OK=false with a LastError", h)
	}
	cl2 := snap.Health[SourceContainerlab]
	if !cl2.Configured || !cl2.OK {
		t.Errorf("Containerlab health = %+v, want Configured=true OK=true", cl2)
	}
}

// TestAggregator_Snapshot_FallsBackToCacheOnFailure verifies that once a
// source has answered successfully at least once, a later failure does not
// drop its data from the Snapshot — the last good value is kept, with
// Health reporting the source as currently unreachable.
func TestAggregator_Snapshot_FallsBackToCacheOnFailure(t *testing.T) {
	var fail atomic.Bool
	pluginMux := http.NewServeMux()
	pluginMux.HandleFunc("GET /plugin/status", func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(pluginStatusFixture))
	})
	pluginSrv := httptest.NewServer(pluginMux)
	defer pluginSrv.Close()

	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{{Name: "node3", State: "running"}},
	}}
	agg := New(cl, nil, plugin.NewClient(pluginSrv.URL, nil))

	snap1, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot (1st): %v", err)
	}
	if snap1.Nodes[0].Plugin == nil {
		t.Fatalf("expected a plugin overlay on the first (successful) snapshot")
	}
	if !snap1.Health[SourcePlugin].OK {
		t.Errorf("Health[plugin] = %+v, want OK=true after a successful poll", snap1.Health[SourcePlugin])
	}

	fail.Store(true)
	snap2, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot (2nd): %v", err)
	}
	if snap2.Nodes[0].Plugin == nil {
		t.Errorf("expected the cached plugin overlay to survive a failed poll, got none")
	}
	h := snap2.Health[SourcePlugin]
	if h.OK || h.LastError == "" {
		t.Errorf("Health[plugin] = %+v, want OK=false with LastError set after the failure", h)
	}
	if h.LastSuccess == nil {
		t.Errorf("Health[plugin].LastSuccess should still be set from the earlier success, got nil")
	}
}

// TestAggregator_Snapshot_HealthDistinguishesUnconfiguredFromUnreachable
// verifies that a source that was never given a client (not configured for
// this deployment) is reported differently from one that is configured but
// currently failing.
func TestAggregator_Snapshot_HealthDistinguishesUnconfiguredFromUnreachable(t *testing.T) {
	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{{Name: "solo", State: "running"}},
	}}
	agg := New(cl, nil, nil)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for _, name := range []SourceName{SourceSakutei, SourcePlugin} {
		h := snap.Health[name]
		if h.Configured {
			t.Errorf("Health[%s].Configured = true, want false (no client was given)", name)
		}
	}
	if h := snap.Health[SourceContainerlab]; !h.Configured || !h.OK {
		t.Errorf("Health[containerlab] = %+v, want Configured=true OK=true", h)
	}
}

// newAggregatorForBindingTest builds an Aggregator with a containerlab
// source fixed to the given nodes, a Sakutei /status handler answering with
// sakuteiResourcesJSON (wrapped in a minimal otherwise-empty envelope), and
// -- if pluginStatusJSON is non-empty -- a plugin /plugin/status handler
// answering with that raw JSON body verbatim (so callers can exercise both
// "sakutei_bindings present" and "field absent entirely", the latter
// standing in for a pre-v0.3 plugin).
func newAggregatorForBindingTest(t *testing.T, nodeNames []string, sakuteiResourcesJSON string, pluginStatusJSON string) *Aggregator {
	t.Helper()

	sakuteiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"schema_version": 1,
			"generated_at": "2026-10-28T00:00:00Z",
			"complete": true,
			"cycle": 1,
			"cycle_time": "2026-10-28T00:00:00Z",
			"last_cycle": 1,
			"summary": {"conformant": 0, "drift": 0, "missing": 0, "unobservable": 0,
				"unmanaged": 0, "workflow": 0, "oscillating": 0, "list_errors": 0, "total": 0},
			"resources": [` + sakuteiResourcesJSON + `]
		}`))
	}))
	t.Cleanup(sakuteiSrv.Close)

	var pluginClient *plugin.Client
	if pluginStatusJSON != "" {
		pluginSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(pluginStatusJSON))
		}))
		t.Cleanup(pluginSrv.Close)
		pluginClient = plugin.NewClient(pluginSrv.URL, nil)
	}

	nodes := make([]containerlab.Node, len(nodeNames))
	for i, n := range nodeNames {
		nodes[i] = containerlab.Node{Name: n, State: "running"}
	}
	cl := mockContainerlabSource{topo: containerlab.Topology{Nodes: nodes}}

	return New(cl, sakutei.NewClient(sakuteiSrv.URL, nil), pluginClient)
}

// TestAggregator_Snapshot_SakuteiBindingByLocalIDPrefix verifies the
// generic matching rule: a resource whose local_id is scoped under a node
// (e.g. a firewall rule nested under "<node>/<family>/<table>/<chain>",
// matching Sakutei's real-world local_id shape for such resources) overlays
// that node even though local_id is not itself equal to the node's name.
func TestAggregator_Snapshot_SakuteiBindingByLocalIDPrefix(t *testing.T) {
	agg := newAggregatorForBindingTest(t, []string{"fw1"},
		`{"id": "res-1", "provider": "nftables", "scope": {}, "local_id": "fw1/inet/filter/forward", "state": "drift"}`,
		"")

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].Sakutei == nil {
		t.Fatalf("Nodes = %+v, want one node with a Sakutei overlay", snap.Nodes)
	}
	if got := snap.Nodes[0].Sakutei; got.ResourceID != "res-1" || got.State != "drift" {
		t.Errorf("fw1.Sakutei = %+v, want ResourceID=res-1 State=drift", got)
	}
}

// TestAggregator_Snapshot_SakuteiExplicitBindingForNonPrefixedLocalID
// verifies that a plugin's optional sakutei_bindings (contract v0.3) can
// bind a resource the generic prefix rule cannot place at all -- e.g. a
// local_id with no node-name segment whatsoever (a DNS zone, an account,
// ...).
func TestAggregator_Snapshot_SakuteiExplicitBindingForNonPrefixedLocalID(t *testing.T) {
	agg := newAggregatorForBindingTest(t, []string{"dns1"},
		`{"id": "res-1", "provider": "dns", "scope": {}, "local_id": "example.test./A", "state": "conformant"}`,
		`{"generated_at": "2026-10-28T00:00:00Z", "resources": [],
		  "sakutei_bindings": [{"resource_id": "res-1", "node": "dns1"}]}`)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].Sakutei == nil {
		t.Fatalf("Nodes = %+v, want dns1 to have a Sakutei overlay via the explicit binding", snap.Nodes)
	}
	if got := snap.Nodes[0].Sakutei.ResourceID; got != "res-1" {
		t.Errorf("dns1.Sakutei.ResourceID = %q, want res-1", got)
	}
}

// TestAggregator_Snapshot_SakuteiExplicitBindingOverridesGeneric verifies
// that when a plugin's sakutei_bindings names a different node than the
// generic local_id-prefix rule would, the explicit binding wins.
func TestAggregator_Snapshot_SakuteiExplicitBindingOverridesGeneric(t *testing.T) {
	agg := newAggregatorForBindingTest(t, []string{"fw1", "dns1"},
		`{"id": "res-1", "provider": "nftables", "scope": {}, "local_id": "fw1/inet/filter/forward", "state": "drift"}`,
		`{"generated_at": "2026-10-28T00:00:00Z", "resources": [],
		  "sakutei_bindings": [{"resource_id": "res-1", "node": "dns1"}]}`)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	byName := map[string]Node{}
	for _, n := range snap.Nodes {
		byName[n.Name] = n
	}
	if byName["fw1"].Sakutei != nil {
		t.Errorf("fw1.Sakutei = %+v, want nil (explicit binding should have moved it to dns1)", byName["fw1"].Sakutei)
	}
	if byName["dns1"].Sakutei == nil || byName["dns1"].Sakutei.ResourceID != "res-1" {
		t.Errorf("dns1.Sakutei = %+v, want ResourceID=res-1", byName["dns1"].Sakutei)
	}
}

// TestAggregator_Snapshot_SakuteiBindingPluginWithoutBindingsField verifies
// backward compatibility with a v0.1/v0.2 plugin, whose /plugin/status
// response has no sakutei_bindings field at all: the generic local_id-prefix
// rule must still apply to every resource, exactly as if no plugin were
// configured.
func TestAggregator_Snapshot_SakuteiBindingPluginWithoutBindingsField(t *testing.T) {
	agg := newAggregatorForBindingTest(t, []string{"fw1"},
		`{"id": "res-1", "provider": "nftables", "scope": {}, "local_id": "fw1/inet/filter/forward", "state": "drift"}`,
		`{"generated_at": "2026-10-28T00:00:00Z", "resources": []}`)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].Sakutei == nil || snap.Nodes[0].Sakutei.ResourceID != "res-1" {
		t.Fatalf("Nodes = %+v, want fw1 bound via the generic rule despite the plugin being configured", snap.Nodes)
	}
}

// TestAggregator_Snapshot_HealthReflectsAnySakuteiEndpointFailing is the
// regression test for a bug where sakutei-view's three independent polls of
// Sakutei (/status, /plan, /graph) each wrote to the same
// Health[SourceSakutei] entry: whichever one ran last always won, so a
// failing /status could be silently masked by a succeeding /plan or /graph
// polled afterwards within the same Snapshot call. Health must reflect the
// failure even though two of the three endpoints succeeded.
func TestAggregator_Snapshot_HealthReflectsAnySakuteiEndpointFailing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "boom"}`))
	})
	mux.HandleFunc("GET /plan", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sakuteiPlanFixture))
	})
	mux.HandleFunc("GET /graph", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sakuteiGraphFixture))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{{Name: "leaf1", State: "running"}, {Name: "spine1", State: "running"}},
	}}
	agg := New(cl, sakutei.NewClient(srv.URL, nil), nil)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// /plan and /graph still succeeded, so their data should be present...
	if len(snap.SakuteiStopReasons) == 0 {
		t.Errorf("SakuteiStopReasons = %+v, want the budget/prune halts from the successful /plan poll", snap.SakuteiStopReasons)
	}
	if snap.SakuteiGraph == nil {
		t.Errorf("SakuteiGraph = nil, want the successful /graph poll's data")
	}
	// ...but /status failed, so Health must show Sakutei as failed overall,
	// not OK just because the other two endpoints answered.
	h := snap.Health[SourceSakutei]
	if !h.Configured {
		t.Fatalf("Health[sakutei].Configured = false, want true")
	}
	if h.OK {
		t.Errorf("Health[sakutei].OK = true, want false: /status failed even though /plan and /graph succeeded")
	}
	if h.LastError == "" {
		t.Errorf("Health[sakutei].LastError is empty, want the /status failure's message")
	}
}

// TestAggregator_Snapshot_HealthOKWhenAllSakuteiEndpointsSucceed is the
// positive-case companion to
// TestAggregator_Snapshot_HealthReflectsAnySakuteiEndpointFailing: when all
// three Sakutei polls succeed, Health must show Sakutei as OK.
func TestAggregator_Snapshot_HealthOKWhenAllSakuteiEndpointsSucceed(t *testing.T) {
	agg := newTestAggregator(t)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	h := snap.Health[SourceSakutei]
	if !h.Configured || !h.OK || h.LastError != "" {
		t.Errorf("Health[sakutei] = %+v, want Configured=true OK=true LastError=\"\"", h)
	}
}

func TestAggregator_Snapshot_SakuteiNotYet(t *testing.T) {
	sakuteiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error": "no cycle has observed reality yet"}`))
	}))
	defer sakuteiSrv.Close()

	cl := mockContainerlabSource{topo: containerlab.Topology{
		Nodes: []containerlab.Node{{Name: "solo", State: "running"}},
	}}
	agg := New(cl, sakutei.NewClient(sakuteiSrv.URL, nil), nil)

	snap, err := agg.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.SakuteiSummary != nil {
		t.Errorf("SakuteiSummary = %+v, want nil while daemon has not observed reality yet", snap.SakuteiSummary)
	}
	if snap.Nodes[0].Color() != ColorUnknown {
		t.Errorf("Color = %q, want unknown", snap.Nodes[0].Color())
	}
}
