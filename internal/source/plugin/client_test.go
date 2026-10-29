package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestClient_InfoStatusScripts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /plugin", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Info{Version: "0.1", Name: "test-plugin", Capabilities: []string{"status", "scripts", "events"}})
	})
	mux.HandleFunc("GET /plugin/status", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(StatusResponse{
			GeneratedAt: time.Date(2026, 10, 28, 6, 0, 0, 0, time.UTC),
			Resources: []Resource{
				{ID: "demo-01", Kind: "storage", Label: "Demo", State: "ok", Node: "node01", Fields: map[string]any{"lun_count": float64(4)}},
			},
		})
	})
	mux.HandleFunc("GET /plugin/scripts", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ScriptsResponse{Scripts: []Script{{ID: "lun-drift", Label: "LUN drift", Actions: []string{"disturb", "fix"}}}})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	ctx := context.Background()

	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Name != "test-plugin" || !info.HasCapability("events") {
		t.Fatalf("unexpected Info: %+v", info)
	}

	status, err := c.Status(ctx)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Resources) != 1 || status.Resources[0].ID != "demo-01" {
		t.Fatalf("unexpected Status: %+v", status)
	}

	scripts, err := c.Scripts(ctx)
	if err != nil {
		t.Fatalf("Scripts: %v", err)
	}
	if len(scripts.Scripts) != 1 || scripts.Scripts[0].ID != "lun-drift" {
		t.Fatalf("unexpected Scripts: %+v", scripts)
	}
}

func TestStatusResponse_SakuteiBindingsV03AndV02Compat(t *testing.T) {
	// v0.3: sakutei_bindings present.
	var withBindings StatusResponse
	if err := json.Unmarshal([]byte(`{
		"generated_at": "2026-10-28T06:00:00Z",
		"resources": [],
		"sakutei_bindings": [{"resource_id": "res-1", "node": "fw1"}, {"resource_id": "res-2", "node": "dns1"}]
	}`), &withBindings); err != nil {
		t.Fatalf("Unmarshal (v0.3 with bindings): %v", err)
	}
	if len(withBindings.SakuteiBindings) != 2 ||
		withBindings.SakuteiBindings[0] != (SakuteiBinding{ResourceID: "res-1", Node: "fw1"}) ||
		withBindings.SakuteiBindings[1] != (SakuteiBinding{ResourceID: "res-2", Node: "dns1"}) {
		t.Fatalf("unexpected decode: %+v", withBindings.SakuteiBindings)
	}

	// v0.1/v0.2: no sakutei_bindings field at all. Must decode to a nil
	// (not empty-non-nil) slice, same as a pre-v0.3 response always did.
	var v02 StatusResponse
	if err := json.Unmarshal([]byte(`{
		"generated_at": "2026-10-28T06:00:00Z",
		"resources": [{"id": "demo-01", "kind": "storage", "label": "Demo", "state": "ok", "node": "node01"}]
	}`), &v02); err != nil {
		t.Fatalf("Unmarshal (v0.2, no sakutei_bindings): %v", err)
	}
	if v02.SakuteiBindings != nil {
		t.Fatalf("SakuteiBindings = %+v, want nil when the field is absent", v02.SakuteiBindings)
	}
	if len(v02.Resources) != 1 || v02.Resources[0].ID != "demo-01" {
		t.Fatalf("unexpected Resources decode: %+v", v02.Resources)
	}
}

func TestStatusResponse_SakuteiSourcesAndPolicySourcesV04(t *testing.T) {
	// v0.4: both fields present, including the derived (file=null) and
	// why=null cases a UI must still show (not hide).
	var withSources StatusResponse
	if err := json.Unmarshal([]byte(`{
		"generated_at": "2026-10-28T06:00:00Z",
		"resources": [],
		"sakutei_sources": [
			{"resource_id": "res-1", "file": "network.yml", "key": "resources[name=res-1]", "why": "reachability"},
			{"resource_id": "res-2", "file": null, "key": "", "why": null, "derived": true, "derived_from": ["network.yml"]}
		],
		"policy_sources": [
			{"name": "rule-1", "from": "a0", "to": "fw1", "proto": "tcp", "ports": [22], "file": "network.yml", "key": "firewall.rules[name=rule-1]", "why": "ssh mgmt"}
		]
	}`), &withSources); err != nil {
		t.Fatalf("Unmarshal (v0.4 with sources): %v", err)
	}
	if len(withSources.SakuteiSources) != 2 {
		t.Fatalf("SakuteiSources = %+v, want 2 entries", withSources.SakuteiSources)
	}
	s0 := withSources.SakuteiSources[0]
	if s0.ResourceID != "res-1" || s0.File == nil || *s0.File != "network.yml" || s0.Key != "resources[name=res-1]" || s0.Why == nil || *s0.Why != "reachability" {
		t.Errorf("unexpected SakuteiSources[0]: %+v", s0)
	}
	s1 := withSources.SakuteiSources[1]
	if s1.ResourceID != "res-2" || s1.File != nil || s1.Why != nil || !s1.Derived || len(s1.DerivedFrom) != 1 || s1.DerivedFrom[0] != "network.yml" {
		t.Errorf("unexpected SakuteiSources[1] (derived, file=null, why=null): %+v", s1)
	}
	if len(withSources.PolicySources) != 1 {
		t.Fatalf("PolicySources = %+v, want 1 entry", withSources.PolicySources)
	}
	p0 := withSources.PolicySources[0]
	if p0.Name != "rule-1" || p0.From != "a0" || p0.To != "fw1" || p0.Proto != "tcp" || len(p0.Ports) != 1 || p0.Ports[0] != 22 || p0.File == nil || *p0.File != "network.yml" || p0.Why == nil || *p0.Why != "ssh mgmt" {
		t.Errorf("unexpected PolicySources[0]: %+v", p0)
	}

	// v0.1/v0.2/v0.3: neither field present at all. Must decode to nil (not
	// empty-non-nil) slices, and every other field must decode normally --
	// this is the "field absent" half of the required coverage.
	var noSources StatusResponse
	if err := json.Unmarshal([]byte(`{
		"generated_at": "2026-10-28T06:00:00Z",
		"resources": [{"id": "demo-01", "kind": "storage", "label": "Demo", "state": "ok", "node": "node01"}]
	}`), &noSources); err != nil {
		t.Fatalf("Unmarshal (no sources): %v", err)
	}
	if noSources.SakuteiSources != nil {
		t.Errorf("SakuteiSources = %+v, want nil when the field is absent", noSources.SakuteiSources)
	}
	if noSources.PolicySources != nil {
		t.Errorf("PolicySources = %+v, want nil when the field is absent", noSources.PolicySources)
	}
	if len(noSources.Resources) != 1 || noSources.Resources[0].ID != "demo-01" {
		t.Errorf("unexpected Resources decode: %+v", noSources.Resources)
	}
}

// TestClient_Status_TypeMismatchedFieldDoesNotBreakOthers is the v0.4
// analogue of the resilience the sakutei client already has: if
// sakutei_sources (or any one field) comes back in a shape this client
// doesn't recognize -- e.g. a future/buggy plugin sending "ports" as a
// string instead of an array of numbers inside policy_sources -- the rest
// of StatusResponse (including Resources, which has nothing to do with the
// malformed field) must still decode, via Client.Status rather than a bare
// json.Unmarshal, to exercise decodeJSON's partial-decode tolerance.
func TestClient_Status_TypeMismatchedFieldDoesNotBreakOthers(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /plugin/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"generated_at": "2026-10-28T06:00:00Z",
			"resources": [{"id": "demo-01", "kind": "storage", "label": "Demo", "state": "ok", "node": "node01"}],
			"policy_sources": [
				{"name": "rule-1", "from": "a0", "to": "fw1", "proto": "tcp", "ports": "not-an-array", "file": "network.yml", "key": "x", "why": null}
			]
		}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v, want the malformed policy_sources[0].ports field to be skipped rather than failing the whole decode", err)
	}
	if len(status.Resources) != 1 || status.Resources[0].ID != "demo-01" {
		t.Errorf("Resources = %+v, want the unrelated field to still decode correctly", status.Resources)
	}
}

func TestClient_RunAction_AcceptedAndConflict(t *testing.T) {
	var runInProgress bool
	mux := http.NewServeMux()
	mux.HandleFunc("POST /plugin/scripts/lun-drift/actions/disturb", func(w http.ResponseWriter, r *http.Request) {
		if runInProgress {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(ActionResult{Accepted: false, RunID: "r-0001", Reason: "another run is already in progress"})
			return
		}
		runInProgress = true
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(ActionResult{Accepted: true, RunID: "r-0001"})
	})
	mux.HandleFunc("GET /plugin/runs/r-0001", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Run{RunID: "r-0001", ScriptID: "lun-drift", Action: "disturb", Status: "succeeded"})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(srv.URL, srv.Client())
	ctx := context.Background()

	res, err := c.RunAction(ctx, "lun-drift", "disturb")
	if err != nil {
		t.Fatalf("RunAction (first): %v", err)
	}
	if !res.Accepted || res.RunID != "r-0001" {
		t.Fatalf("unexpected first RunAction result: %+v", res)
	}

	res2, err := c.RunAction(ctx, "lun-drift", "disturb")
	if err != nil {
		t.Fatalf("RunAction (second, should be 409 but not a Go error): %v", err)
	}
	if res2.Accepted {
		t.Fatalf("expected second RunAction to report not-accepted (409), got: %+v", res2)
	}

	run, err := c.Run(ctx, res.RunID)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run.Status != "succeeded" {
		t.Fatalf("unexpected run status: %+v", run)
	}
}

func TestActionResult_V02FieldsAndV01Compat(t *testing.T) {
	// v0.2: external busy, no run_id.
	var external ActionResult
	if err := json.Unmarshal([]byte(`{"accepted":false,"reason":"environment rebuild in progress","busy_by":"external"}`), &external); err != nil {
		t.Fatalf("Unmarshal (external): %v", err)
	}
	if external.Accepted || external.RunID != "" || external.BusyBy != "external" || external.Reason == "" {
		t.Fatalf("unexpected decode: %+v", external)
	}

	// v0.2: busy by one of the plugin's own runs.
	var ownRun ActionResult
	if err := json.Unmarshal([]byte(`{"accepted":false,"reason":"another run is already in progress","run_id":"r-0001","busy_by":"run"}`), &ownRun); err != nil {
		t.Fatalf("Unmarshal (own run): %v", err)
	}
	if ownRun.RunID != "r-0001" || ownRun.BusyBy != "run" {
		t.Fatalf("unexpected decode: %+v", ownRun)
	}

	// v0.1 plugin: no busy_by field at all, run_id always set. A v0.2-aware
	// client must still decode this correctly, with BusyBy left empty (a
	// caller is expected to treat an empty BusyBy as "run").
	var v01 ActionResult
	if err := json.Unmarshal([]byte(`{"accepted":false,"reason":"another run is already in progress","run_id":"r-0001"}`), &v01); err != nil {
		t.Fatalf("Unmarshal (v0.1): %v", err)
	}
	if v01.RunID != "r-0001" || v01.BusyBy != "" {
		t.Fatalf("unexpected decode of a v0.1-shaped response: %+v", v01)
	}
}

func TestRun_StatusV02ValuesAndUnknown(t *testing.T) {
	cases := []struct {
		status     string
		inProgress bool
	}{
		{"running", true},
		{"succeeded", false},
		{"failed", false},
		{"rejected", false},           // added in v0.2
		{"cancelled", false},          // added in v0.2
		{"some-future-status", false}, // unrecognized: must still be treated as terminal
	}
	for _, c := range cases {
		var r Run
		body := fmt.Sprintf(`{"run_id":"r-1","script_id":"s","action":"disturb","status":%q,"started_at":"2026-10-28T06:00:00Z","finished_at":null,"message":""}`, c.status)
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("Unmarshal status=%q: %v", c.status, err)
		}
		if r.Status != c.status {
			t.Errorf("status round-trip: got %q, want %q", r.Status, c.status)
		}
		if got := RunIsInProgress(r.Status); got != c.inProgress {
			t.Errorf("RunIsInProgress(%q) = %v, want %v", c.status, got, c.inProgress)
		}
	}
}

func TestClient_Events_StreamAndResumeWithLastEventID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /plugin/events", func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		lastID := r.Header.Get("Last-Event-ID")
		start := 1
		if lastID == "1" {
			start = 2 // resume: only replay what comes after id=1
		}
		for i := start; i <= 2; i++ {
			ev := Event{Kind: "Disturbed", Level: "firing", ResourceID: "demo-01", Message: fmt.Sprintf("event %d", i), At: time.Now().UTC()}
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", i, b)
			flusher.Flush()
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewClient(srv.URL, srv.Client())

	t.Run("from the start", func(t *testing.T) {
		var mu sync.Mutex
		var gotIDs []string
		err := c.Events(context.Background(), "", func(id string, ev Event) error {
			mu.Lock()
			gotIDs = append(gotIDs, id)
			mu.Unlock()
			return nil
		})
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		if len(gotIDs) != 2 || gotIDs[0] != "1" || gotIDs[1] != "2" {
			t.Fatalf("unexpected event ids: %v", gotIDs)
		}
	})

	t.Run("resuming after Last-Event-ID", func(t *testing.T) {
		var gotIDs []string
		err := c.Events(context.Background(), "1", func(id string, ev Event) error {
			gotIDs = append(gotIDs, id)
			return nil
		})
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		if len(gotIDs) != 1 || gotIDs[0] != "2" {
			t.Fatalf("expected resume to only replay event 2, got: %v", gotIDs)
		}
	})
}
