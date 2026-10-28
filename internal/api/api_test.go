package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tjst-t/sakutei-view/internal/aggregate"
	"github.com/tjst-t/sakutei-view/internal/source/containerlab"
	"github.com/tjst-t/sakutei-view/internal/source/plugin"
)

// stubAggregator is a fixed Aggregator for tests, standing in for
// *aggregate.Aggregator without any real HTTP sources behind it.
type stubAggregator struct {
	snapshot    aggregate.Snapshot
	snapshotErr error
	events      []aggregate.Event
	eventsErr   error
}

func (s *stubAggregator) Snapshot(ctx context.Context) (aggregate.Snapshot, error) {
	return s.snapshot, s.snapshotErr
}

func (s *stubAggregator) Events(ctx context.Context, onEvent func(aggregate.Event) error) error {
	for _, ev := range s.events {
		if err := onEvent(ev); err != nil {
			return err
		}
	}
	return s.eventsErr
}

func testSnapshot() aggregate.Snapshot {
	return aggregate.Snapshot{
		GeneratedAt: time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC),
		Nodes: []aggregate.Node{
			{Node: containerlab.Node{Name: "leaf1", State: "running"},
				Sakutei: &aggregate.SakuteiOverlay{ResourceID: "res-leaf1", State: "conformant"}},
			{Node: containerlab.Node{Name: "down-node", State: "exited"}},
		},
		Links: []containerlab.Link{
			{Type: "veth",
				A: containerlab.LinkEndpoint{Node: "leaf1", Interface: "eth0"},
				B: containerlab.LinkEndpoint{Node: "down-node", Interface: "eth0"}},
		},
	}
}

func TestServer_HandleSnapshot(t *testing.T) {
	srv := &Server{Aggregator: &stubAggregator{snapshot: testSnapshot()}}
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
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var body struct {
		GeneratedAt time.Time `json:"generated_at"`
		Nodes       []struct {
			Name    string                    `json:"name"`
			State   string                    `json:"state"`
			Color   aggregate.Color           `json:"color"`
			Sakutei *aggregate.SakuteiOverlay `json:"sakutei"`
		} `json:"nodes"`
		Links []containerlab.Link `json:"links"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}

	if len(body.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2", len(body.Nodes))
	}
	if body.Nodes[0].Name != "leaf1" || body.Nodes[0].Color != aggregate.ColorOK || body.Nodes[0].Sakutei == nil {
		t.Errorf("Nodes[0] = %+v, unexpected", body.Nodes[0])
	}
	if body.Nodes[1].Name != "down-node" || body.Nodes[1].Color != aggregate.ColorDown {
		t.Errorf("Nodes[1] = %+v, unexpected", body.Nodes[1])
	}
	if len(body.Links) != 1 || body.Links[0].A.Node != "leaf1" || body.Links[0].B.Node != "down-node" {
		t.Errorf("Links = %+v, unexpected", body.Links)
	}
}

func TestServer_HandleSnapshot_AggregatorError(t *testing.T) {
	srv := &Server{Aggregator: &stubAggregator{snapshotErr: errors.New("boom")}}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/snapshot")
	if err != nil {
		t.Fatalf("GET /api/snapshot: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", resp.StatusCode)
	}
}

func TestServer_HandleEvents(t *testing.T) {
	evs := []aggregate.Event{
		{Source: aggregate.EventSourceSakutei, Time: time.Now(), Kind: "drift", Resource: "leaf1", Message: "drifted", State: "firing"},
		{Source: aggregate.EventSourcePlugin, Time: time.Now(), Kind: "restart", Resource: "node3", Message: "restarted"},
	}
	srv := &Server{Aggregator: &stubAggregator{events: evs}}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	events, err := decodeSSEEvents(resp)
	if err != nil {
		t.Fatalf("decodeSSEEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d SSE events, want 2: %+v", len(events), events)
	}
	if events[0].Kind != "drift" || events[0].Resource != "leaf1" || events[0].State != "firing" {
		t.Errorf("events[0] = %+v, unexpected", events[0])
	}
	if events[1].Kind != "restart" || events[1].Resource != "node3" {
		t.Errorf("events[1] = %+v, unexpected", events[1])
	}
}

// decodeSSEEvents does minimal SSE framing (blank-line-terminated "data:"
// lines) to recover the JSON events the server wrote.
func decodeSSEEvents(resp *http.Response) ([]aggregate.Event, error) {
	scanner := bufio.NewScanner(resp.Body)
	var events []aggregate.Event
	var data string
	flush := func() error {
		if data == "" {
			return nil
		}
		var ev aggregate.Event
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return err
		}
		events = append(events, ev)
		data = ""
		return nil
	}
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if err := flush(); err != nil {
				return nil, err
			}
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return events, scanner.Err()
}

func TestServer_BasicAuth(t *testing.T) {
	srv := &Server{
		Aggregator:        &stubAggregator{snapshot: testSnapshot()},
		BasicAuthUser:     "u",
		BasicAuthPassword: "p",
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/snapshot")
	if err != nil {
		t.Fatalf("GET /api/snapshot: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without credentials = %d, want 401", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/snapshot", nil)
	req.SetBasicAuth("u", "p")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/snapshot with auth: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status with credentials = %d, want 200", resp2.StatusCode)
	}
}

// newTestPluginServer stands up a minimal fake plugin HTTP server
// implementing just enough of docs/plugin-contract.md (v0.2) for the
// actions-route tests below: GET /plugin/scripts, POST
// /plugin/scripts/{id}/actions/{action} (202 the first time, 409 with
// busy_by "run" while that run is still "running"), and GET
// /plugin/runs/{id} (404 for an unknown id, otherwise a fixed running run).
func newTestPluginServer(t *testing.T) *httptest.Server {
	t.Helper()
	const knownRunID = "run-1"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /plugin/scripts", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(plugin.ScriptsResponse{
			Scripts: []plugin.Script{{ID: "demo", Label: "Demo", Actions: []string{"disturb", "fix"}}},
		})
	})
	ran := false
	mux.HandleFunc("POST /plugin/scripts/{scriptID}/actions/{action}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if ran {
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(plugin.ActionResult{
				Accepted: false,
				Reason:   "another run is already in progress",
				RunID:    knownRunID,
				BusyBy:   "run",
			})
			return
		}
		ran = true
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(plugin.ActionResult{Accepted: true, RunID: knownRunID})
	})
	mux.HandleFunc("GET /plugin/runs/{runID}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("runID") != knownRunID {
			http.Error(w, "no such run", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(plugin.Run{
			RunID: knownRunID, ScriptID: "demo", Action: "disturb", Status: "running", StartedAt: time.Now(),
		})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestServer_ActionsRoutes_DisabledWithoutBasicAuth(t *testing.T) {
	pluginSrv := newTestPluginServer(t)
	srv := &Server{
		Aggregator: &stubAggregator{snapshot: testSnapshot()},
		Plugin:     plugin.NewClient(pluginSrv.URL, nil),
		// No BasicAuthUser/BasicAuthPassword: actionsEnabled() must be
		// false even though a plugin is configured, so the routes below
		// must not even be registered (plain 404, not 401/403).
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/scripts"},
		{http.MethodPost, "/api/actions/demo/disturb"},
		{http.MethodGet, "/api/runs/run-1"},
	} {
		r, err := http.NewRequest(req.method, ts.URL+req.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatalf("%s %s: %v", req.method, req.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 (actions must be unregistered without Basic auth)", req.method, req.path, resp.StatusCode)
		}
	}

	// Snapshot must still report actions_enabled: false.
	resp, err := http.Get(ts.URL + "/api/snapshot")
	if err != nil {
		t.Fatalf("GET /api/snapshot: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		ActionsEnabled bool `json:"actions_enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding snapshot: %v", err)
	}
	if body.ActionsEnabled {
		t.Errorf("actions_enabled = true, want false (no Basic auth configured)")
	}
}

func TestServer_ActionsRoutes_DisabledWithoutPlugin(t *testing.T) {
	srv := &Server{
		Aggregator:        &stubAggregator{snapshot: testSnapshot()},
		BasicAuthUser:     "u",
		BasicAuthPassword: "p",
		// No Plugin: actionsEnabled() must be false even though Basic auth
		// is configured.
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/scripts", nil)
	req.SetBasicAuth("u", "p")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/scripts: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/scripts = %d, want 404 (no plugin configured)", resp.StatusCode)
	}
}

func TestServer_ActionsRoutes_Enabled(t *testing.T) {
	pluginSrv := newTestPluginServer(t)
	srv := &Server{
		Aggregator:        &stubAggregator{snapshot: testSnapshot()},
		Plugin:            plugin.NewClient(pluginSrv.URL, nil),
		BasicAuthUser:     "u",
		BasicAuthPassword: "p",
	}
	mux := http.NewServeMux()
	srv.RegisterRoutes(mux)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	do := func(method, path string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth("u", "p")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// actions_enabled: true in the snapshot.
	snapResp := do(http.MethodGet, "/api/snapshot")
	var snapBody struct {
		ActionsEnabled bool `json:"actions_enabled"`
	}
	if err := json.NewDecoder(snapResp.Body).Decode(&snapBody); err != nil {
		t.Fatalf("decoding snapshot: %v", err)
	}
	snapResp.Body.Close()
	if !snapBody.ActionsEnabled {
		t.Errorf("actions_enabled = false, want true (plugin + Basic auth both configured)")
	}

	// GET /api/scripts proxies the plugin's script list.
	scriptsResp := do(http.MethodGet, "/api/scripts")
	if scriptsResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/scripts = %d, want 200", scriptsResp.StatusCode)
	}
	var scripts plugin.ScriptsResponse
	if err := json.NewDecoder(scriptsResp.Body).Decode(&scripts); err != nil {
		t.Fatalf("decoding scripts: %v", err)
	}
	scriptsResp.Body.Close()
	if len(scripts.Scripts) != 1 || scripts.Scripts[0].ID != "demo" {
		t.Errorf("scripts = %+v, unexpected", scripts)
	}

	// First POST /api/actions/{scriptID}/{action} accepts (202).
	firstResp := do(http.MethodPost, "/api/actions/demo/disturb")
	if firstResp.StatusCode != http.StatusAccepted {
		t.Fatalf("first POST /api/actions = %d, want 202", firstResp.StatusCode)
	}
	var firstResult plugin.ActionResult
	if err := json.NewDecoder(firstResp.Body).Decode(&firstResult); err != nil {
		t.Fatalf("decoding action result: %v", err)
	}
	firstResp.Body.Close()
	if !firstResult.Accepted || firstResult.RunID != "run-1" {
		t.Errorf("first action result = %+v, unexpected", firstResult)
	}

	// Second POST while the run is in progress conflicts (409), carrying
	// reason and busy_by (v0.2) as the UI needs to display them.
	secondResp := do(http.MethodPost, "/api/actions/demo/disturb")
	if secondResp.StatusCode != http.StatusConflict {
		t.Fatalf("second POST /api/actions = %d, want 409", secondResp.StatusCode)
	}
	var secondResult plugin.ActionResult
	if err := json.NewDecoder(secondResp.Body).Decode(&secondResult); err != nil {
		t.Fatalf("decoding action result: %v", err)
	}
	secondResp.Body.Close()
	if secondResult.Accepted || secondResult.BusyBy != "run" || secondResult.RunID != "run-1" || secondResult.Reason == "" {
		t.Errorf("second (conflicting) action result = %+v, unexpected", secondResult)
	}

	// GET /api/runs/{runID} proxies the run's status.
	runResp := do(http.MethodGet, "/api/runs/run-1")
	if runResp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/runs/run-1 = %d, want 200", runResp.StatusCode)
	}
	var run plugin.Run
	if err := json.NewDecoder(runResp.Body).Decode(&run); err != nil {
		t.Fatalf("decoding run: %v", err)
	}
	runResp.Body.Close()
	if run.RunID != "run-1" || run.Status != "running" {
		t.Errorf("run = %+v, unexpected", run)
	}

	// GET /api/runs/{unknown} forwards the plugin's own 404, via
	// plugin.StatusCodeOf, rather than defaulting to 502.
	missingResp := do(http.MethodGet, "/api/runs/no-such-run")
	defer missingResp.Body.Close()
	if missingResp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /api/runs/no-such-run = %d, want 404", missingResp.StatusCode)
	}
}
