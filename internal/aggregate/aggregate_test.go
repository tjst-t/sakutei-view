package aggregate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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

func newTestAggregator(t *testing.T) *Aggregator {
	t.Helper()

	sakuteiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(sakuteiStatusFixture))
	}))
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
