package sakutei

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Fixtures below mirror the shapes documented in docs/status-json.md,
// docs/plan-json.md and docs/read-api.md (as of the v2.5.0 tag).

const statusFixture = `{
  "schema_version": 1,
  "generated_at": "2026-10-27T08:18:54Z",
  "intent_revision": "520eabc",
  "rendered_revision": "520eabc",
  "complete": true,
  "cycle": 42,
  "cycle_time": "2026-10-27T08:18:50Z",
  "last_cycle": 42,
  "summary": {
    "conformant": 3, "drift": 1, "missing": 0, "unobservable": 0,
    "unmanaged": 0, "workflow": 0, "oscillating": 0, "list_errors": 0, "total": 4
  },
  "resources": [
    {"id": "fake/site=a/web", "provider": "fake", "scope": {"site": "a"}, "local_id": "web", "state": "conformant"},
    {"id": "fake/site=a/storage01", "provider": "fake", "scope": {"site": "a"}, "local_id": "storage01",
     "state": "drift", "cause": "C", "fields": [{"field": "lun_count", "from": "6", "to": "4"}],
     "diagnostic": "lun_count differs",
     "tier": {"true_observe": true, "list": true, "bulk_observe": false, "pruneable": true, "provider_version": "1.4.0"}}
  ],
  "daemon": {
    "cycle": 42, "consecutive_unreachable_skips": 0, "unreachable_skip_limit": 5,
    "consecutive_error_cycles": 0, "interval": "10s", "updated_at": "2026-10-27T08:18:50Z",
    "prolonged": false, "stale": false
  }
}`

const planFixture = `{
  "schema_version": 1,
  "generated_at": "2026-10-27T14:40:31Z",
  "intent_revision": "a41dabc",
  "rendered_revision": "a41dabc",
  "cycle": 42,
  "cycle_time": "2026-10-27T08:18:50Z",
  "last_cycle": 42,
  "remediation": {"mode": "auto", "reason": "no schedule"},
  "summary": {"changes": 1, "create": 0, "update": 1, "delete": 0, "destructive": 0, "blocked": 0,
              "planned_change_lane": 0, "task": 0, "workflow": 0, "pending_allocations": 0,
              "unobservable": 0, "prune_halts": 0},
  "operations": [
    {"id": "fake/site=a/storage01", "provider": "fake", "scope": {"site": "a"}, "local_id": "storage01",
     "kind": "UPDATE", "destructive": false, "prune": false,
     "fields": [{"field": "lun_count", "from": "6", "to": "4"}],
     "lane": "normal", "blocked": false}
  ]
}`

const graphFixture = `{
  "schema_version": 1,
  "generated_at": "2026-10-27T15:06:37Z",
  "intent_revision": "85ffabc",
  "rendered_revision": "85ffabc",
  "cycle": 42,
  "cycle_time": "2026-10-27T08:18:50Z",
  "last_cycle": 42,
  "nodes": [
    {"id": "fake//vm-1", "provider": "fake", "local_id": "vm-1", "depends_on": [], "observed": true,
     "conditions": {"cloud_init_done": true}, "ready": true, "wave": 0},
    {"id": "fake//svc-1", "provider": "fake", "local_id": "svc-1", "depends_on": ["vm-1"], "observed": true,
     "ready": true, "wave": 1}
  ],
  "edges": [
    {"from": "vm-1", "to": "svc-1", "ready_condition": "cloud_init_done", "ready": true}
  ],
  "waves": [["vm-1"], ["svc-1"]],
  "summary": {"nodes": 2, "external": 0, "edges": 1, "waves": 2, "not_ready": 0}
}`

func TestClient_Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statusFixture))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Cycle != 42 || status.Summary.Total != 4 {
		t.Fatalf("unexpected top-level fields: %+v", status)
	}
	if len(status.Resources) != 2 {
		t.Fatalf("unexpected resources: %+v", status.Resources)
	}
	drifted := status.Resources[1]
	if drifted.State != "drift" || len(drifted.Fields) != 1 || drifted.Fields[0].Field != "lun_count" {
		t.Fatalf("unexpected drifted resource: %+v", drifted)
	}
	if drifted.Tier == nil || !drifted.Tier.TrueObserve || drifted.Tier.BulkObserve || drifted.Tier.ProviderVersion != "1.4.0" {
		t.Fatalf("unexpected tier: %+v", drifted.Tier)
	}
	if status.Daemon == nil || status.Daemon.Interval != "10s" {
		t.Fatalf("unexpected daemon health: %+v", status.Daemon)
	}
}

// statusFixtureUnknownTierShape is statusFixture's drifted resource, but
// with "tier" in the shape a previous (buggy) version of this package
// itself declared -- a bare string -- instead of the object Sakutei v2.6.0
// actually sends. It stands in for "any future field whose JSON shape this
// package's Go type doesn't expect" (a daemon upgrade, a field this package
// hasn't caught up to yet, ...): decodeJSON must not throw away the whole
// response over it.
const statusFixtureUnknownTierShape = `{
  "schema_version": 1,
  "generated_at": "2026-10-27T08:18:54Z",
  "complete": true,
  "cycle": 42,
  "cycle_time": "2026-10-27T08:18:50Z",
  "last_cycle": 42,
  "summary": {
    "conformant": 3, "drift": 1, "missing": 0, "unobservable": 0,
    "unmanaged": 0, "workflow": 0, "oscillating": 0, "list_errors": 0, "total": 4
  },
  "resources": [
    {"id": "fake/site=a/web", "provider": "fake", "scope": {"site": "a"}, "local_id": "web", "state": "conformant"},
    {"id": "fake/site=a/storage01", "provider": "fake", "scope": {"site": "a"}, "local_id": "storage01",
     "state": "drift", "cause": "C", "fields": [{"field": "lun_count", "from": "6", "to": "4"}],
     "diagnostic": "lun_count differs", "tier": "full"}
  ]
}`

func TestClient_Status_UnknownFieldShapeDoesNotFailWholeDecode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(statusFixtureUnknownTierShape))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v (an unrecognized field shape must not fail the whole decode)", err)
	}
	if status.Cycle != 42 || status.Summary.Total != 4 {
		t.Fatalf("sibling top-level fields were lost: %+v", status)
	}
	if len(status.Resources) != 2 {
		t.Fatalf("sibling array elements were lost: %+v", status.Resources)
	}
	drifted := status.Resources[1]
	if drifted.State != "drift" || drifted.Diagnostic != "lun_count differs" || len(drifted.Fields) != 1 {
		t.Fatalf("sibling fields on the same resource were lost: %+v", drifted)
	}
	// encoding/json allocates the pointer for a struct-pointer field before
	// attempting to decode into it, so Tier ends up non-nil but every field
	// left at its zero value -- it does not get the chance to be populated,
	// but it also must not be some mangled partial value.
	if drifted.Tier == nil {
		t.Fatal("expected Tier to be allocated (zero-valued), got nil")
	}
	if *drifted.Tier != (Tier{}) {
		t.Fatalf("expected Tier to be left zero-valued, got: %+v", drifted.Tier)
	}
}

func TestClient_Plan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(planFixture))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	plan, err := c.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Summary.Changes != 1 || len(plan.Operations) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if plan.Operations[0].Kind != "UPDATE" || plan.Operations[0].Fields[0].To != "4" {
		t.Fatalf("unexpected operation: %+v", plan.Operations[0])
	}
	if plan.Remediation == nil || plan.Remediation.Mode != "auto" {
		t.Fatalf("unexpected remediation: %+v", plan.Remediation)
	}
}

func TestClient_Graph(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(graphFixture))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	graph, err := c.Graph(context.Background())
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if len(graph.Nodes) != 2 || len(graph.Waves) != 2 {
		t.Fatalf("unexpected graph: %+v", graph)
	}
	if graph.Edges[0].Ready == nil || !*graph.Edges[0].Ready {
		t.Fatalf("unexpected edge: %+v", graph.Edges[0])
	}
}

func TestClient_NotYet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "no cycle has observed reality yet"})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())
	_, err := c.Status(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !NotYet(err) {
		t.Fatalf("expected NotYet(err) to be true, got err: %v", err)
	}
}

func TestClient_Events(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		fmt.Fprint(w, ": sakutei events\n\n")
		flusher.Flush()

		firing := Event{Time: "2026-10-27T15:06:37Z", Kind: "Drift", Severity: "warning", Cycle: 4,
			Resource: "fake//web", State: "firing", Since: "2026-10-27T15:00:00Z", Count: 4, Class: "transient"}
		b, _ := json.Marshal(firing)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()

		fmt.Fprint(w, "event: dropped\ndata: {\"dropped\":3}\n\n")
		flusher.Flush()

		resolved := Event{Time: "2026-10-27T15:10:00Z", Kind: "Drift", Severity: "info",
			Resource: "fake//web", State: "resolved"}
		b2, _ := json.Marshal(resolved)
		fmt.Fprintf(w, "data: %s\n\n", b2)
		flusher.Flush()
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.Client())

	var events []Event
	var dropped int64
	err := c.Events(context.Background(), func(ev Event) error {
		events = append(events, ev)
		return nil
	}, func(count int64) {
		dropped = count
	})
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %+v", len(events), events)
	}
	if events[0].State != "firing" || events[1].State != "resolved" {
		t.Fatalf("unexpected event states: %+v", events)
	}
	if dropped != 3 {
		t.Fatalf("expected dropped=3, got %d", dropped)
	}
}
