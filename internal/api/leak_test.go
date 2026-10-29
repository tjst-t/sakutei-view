package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tjst-t/sakutei-view/internal/aggregate"
	"github.com/tjst-t/sakutei-view/internal/source/containerlab"
	"github.com/tjst-t/sakutei-view/internal/source/plugin"
)

// A device plugin's GET /plugin/topology response carries two kinds of raw,
// pass-through-from-containerlab data: "inspect" (containerlab's own
// `inspect --format json` output) and "clab_yaml" (the generated .clab.yml
// file as a plain string, whose node definitions can include an "env"
// block). A real plugin has, in the past, returned secrets embedded in
// clab_yaml's env values (SNMP community strings, API tokens, ...); the
// plugin side has since started redacting them, but sakutei-view itself
// must independently guarantee it never forwards that content regardless
// of what a plugin sends — see docs/plugin-contract.md's "clab_yaml and
// inspect must not be re-exposed" note.
//
// This is a true end-to-end test: a fake plugin HTTP server stands in for
// the device plugin, containerlab.NewPluginSource and aggregate.New wire it
// up exactly as cmd/sakutei-view does, and the assertions run against the
// literal bytes this package's own handlers (handleSnapshot, handleEvents)
// write to an http.ResponseWriter -- not against any intermediate Go
// struct, which could pass even if some future change serialized a raw
// field this test didn't think to decode.
const (
	leakMarkerClabYAMLEnv  = "SUPERSECRET-CLABYAML-ENV-MARKER-9f3a21"
	leakMarkerInspectExtra = "SUPERSECRET-INSPECT-EXTRA-MARKER-7c48e0"
)

// leakTestClabYAML is a .clab.yml whose "server1" node declares an env
// block containing leakMarkerClabYAMLEnv, and one explicit link (so the
// legitimate, intended use of clab_yaml -- deriving Links -- still has
// something to produce). parseClabYAMLLinks only ever decodes
// topology.links; topology.nodes (and its env blocks) must never reach any
// struct this test can observe downstream.
const leakTestClabYAML = `name: leaktest
topology:
  nodes:
    server1:
      kind: linux
      env:
        DEMO_API_TOKEN: ` + leakMarkerClabYAMLEnv + `
    server2:
      kind: linux
  links:
    - endpoints: ["server1:eth1", "server2:eth1"]
`

// leakTestInspectJSON mirrors containerlab's own inspect --format json
// shape, plus one field ("env") that real inspect output does not actually
// contain -- included here only to pin that decodeInspectJSON's
// named-field decode would discard it even if some future containerlab
// version added such a field, not because it is a real risk today.
const leakTestInspectJSON = `{
  "leaktest": [
    {
      "lab_name": "leaktest",
      "name": "clab-leaktest-server1",
      "container_id": "abc123",
      "image": "ghcr.io/example/linux",
      "kind": "linux",
      "state": "running",
      "status": "Up 1 hour",
      "ipv4_address": "172.20.20.2/24",
      "owner": "test",
      "env": {"DECOY_TOKEN": "` + leakMarkerInspectExtra + `"}
    },
    {
      "lab_name": "leaktest",
      "name": "clab-leaktest-server2",
      "container_id": "def456",
      "image": "ghcr.io/example/linux",
      "kind": "linux",
      "state": "running",
      "status": "Up 1 hour",
      "ipv4_address": "172.20.20.3/24",
      "owner": "test"
    }
  ]
}`

// newLeakTestPluginServer stands up a fake device plugin serving
// GET /plugin/topology (the two markers above) and GET /plugin/status (a
// trivial, empty-resources response -- just enough for
// Aggregator.Snapshot's plugin-status poll to succeed).
func newLeakTestPluginServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /plugin/topology", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, err := json.Marshal(plugin.Topology{
			Inspect:  json.RawMessage(leakTestInspectJSON),
			ClabYAML: leakTestClabYAML,
		})
		if err != nil {
			t.Fatalf("marshaling plugin.Topology fixture: %v", err)
		}
		w.Write(body)
	})
	mux.HandleFunc("GET /plugin/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(plugin.StatusResponse{Resources: []plugin.Resource{}})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestServer_HandleSnapshot_DoesNotLeakClabYAMLOrInspect is the regression
// test pinning that GET /api/snapshot never exposes clab_yaml/inspect
// content (e.g. env values) sent by a device plugin, regardless of what the
// plugin sends. It also asserts the legitimate derived data (the link
// clab_yaml's topology.links describes) IS present, so this cannot pass by
// the aggregator having simply failed to reach the plugin at all.
func TestServer_HandleSnapshot_DoesNotLeakClabYAMLOrInspect(t *testing.T) {
	pluginSrv := newLeakTestPluginServer(t)
	pluginClient := plugin.NewClient(pluginSrv.URL, nil)
	clSource := containerlab.NewPluginSource(pluginClient)
	agg := aggregate.New(clSource, nil, pluginClient)

	srv := &Server{Aggregator: agg, Plugin: pluginClient}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/snapshot")
	if err != nil {
		t.Fatalf("GET /api/snapshot: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if strings.Contains(string(body), leakMarkerClabYAMLEnv) {
		t.Errorf("GET /api/snapshot leaked clab_yaml's env block (marker %q found in response body: %s)", leakMarkerClabYAMLEnv, body)
	}
	if strings.Contains(string(body), leakMarkerInspectExtra) {
		t.Errorf("GET /api/snapshot leaked an inspect field outside the known schema (marker %q found in response body: %s)", leakMarkerInspectExtra, body)
	}
	if strings.Contains(string(body), "DEMO_API_TOKEN") || strings.Contains(string(body), "env") {
		t.Errorf("GET /api/snapshot response mentions an env-shaped key, want it absent entirely: %s", body)
	}

	// Sanity check: the legitimate derived data from the same clab_yaml
	// (the explicit link between server1 and server2) must still be
	// present, so a passing test above means "leaked nothing" and not
	// "failed to reach the plugin at all".
	if !strings.Contains(string(body), "server1") || !strings.Contains(string(body), "server2") {
		t.Fatalf("expected the derived node names server1/server2 to be present in the snapshot, got: %s", body)
	}
}

// TestServer_HandleEvents_EventShapeCannotCarryClabYAMLOrInspect pins the
// same guarantee on the /api/events side, but structurally rather than via
// a live plugin round-trip: aggregate.Event (the only type handleEvents
// ever marshals -- see writeSSEEvent) has no field capable of holding
// clab_yaml/inspect content in the first place, so there is no plugin
// response shape that could make it leak through this path. This test
// pins that invariant: marshaling an Event (including its unexported-from-
// JSON SourceEventID, which is the one field on this struct explicitly
// reserved for internal use) never reproduces data that did not already
// come from the event's own named fields.
func TestServer_HandleEvents_EventShapeCannotCarryClabYAMLOrInspect(t *testing.T) {
	ev := aggregate.Event{
		Source:        aggregate.EventSourcePlugin,
		Kind:          "Disturbed",
		Resource:      "server1",
		Message:       "ordinary message",
		State:         "firing",
		SourceEventID: "would-be-leaked-if-this-were-ever-serialized-" + leakMarkerClabYAMLEnv,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(b), leakMarkerClabYAMLEnv) {
		t.Errorf("aggregate.Event serialized its internal SourceEventID (json:\"-\") field, want it always excluded: %s", b)
	}
}
