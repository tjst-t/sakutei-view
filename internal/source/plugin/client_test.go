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
