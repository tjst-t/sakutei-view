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
