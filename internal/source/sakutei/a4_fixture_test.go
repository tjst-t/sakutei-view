package sakutei

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A4 fixtures: sanitized, real-world Sakutei /status, /plan and /graph
// responses collected from an actual Sakutei v2.6.0 deployment (one
// "converged" scenario with no drift, one "drifted" scenario with a single
// resource out of sync), scrubbed of anything internal before being
// checked in under ../../../testdata. Unlike the synthetic fixtures above
// (hand-written to exercise specific shapes), these pin this package's
// decode path against an actual daemon's output end to end, including
// fields a synthetic fixture might not think to include.
const a4TestdataDir = "../../../testdata"

func readA4Fixture(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(a4TestdataDir, rel))
	if err != nil {
		t.Fatalf("reading A4 fixture %s: %v", rel, err)
	}
	return b
}

func serveA4Fixture(t *testing.T, path string, body []byte) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, srv.Client())
}

func TestClient_A4Fixture_ConvergedStatus(t *testing.T) {
	c := serveA4Fixture(t, "/status", readA4Fixture(t, "converged/sakutei-status.sanitized.json"))
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Summary.Total != 34 || status.Summary.Conformant != 34 || status.Summary.Drift != 0 {
		t.Fatalf("unexpected summary: %+v", status.Summary)
	}
	if len(status.Resources) != status.Summary.Total {
		t.Fatalf("resources len %d does not match summary.total %d", len(status.Resources), status.Summary.Total)
	}
	for _, r := range status.Resources {
		if r.State != "conformant" {
			t.Fatalf("expected every resource conformant in the converged fixture, got %q for %s", r.State, r.ID)
		}
	}
	if status.Daemon == nil || status.Daemon.Interval != "30s" {
		t.Fatalf("unexpected daemon health: %+v", status.Daemon)
	}
}

func TestClient_A4Fixture_ConvergedPlan(t *testing.T) {
	c := serveA4Fixture(t, "/plan", readA4Fixture(t, "converged/sakutei-plan.sanitized.json"))
	plan, err := c.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Summary.Changes != 0 || len(plan.Operations) != 0 {
		t.Fatalf("expected no changes in the converged fixture, got: %+v", plan.Summary)
	}
	if plan.Remediation == nil || plan.Remediation.Mode != "alert-only" {
		t.Fatalf("unexpected remediation: %+v", plan.Remediation)
	}
	if plan.Budget == nil || plan.Budget.Halted || plan.Capacity == nil || plan.Capacity.Halted {
		t.Fatalf("expected budget/capacity ungated, got budget=%+v capacity=%+v", plan.Budget, plan.Capacity)
	}
}

func TestClient_A4Fixture_ConvergedGraph(t *testing.T) {
	c := serveA4Fixture(t, "/graph", readA4Fixture(t, "converged/sakutei-graph.sanitized.json"))
	graph, err := c.Graph(context.Background())
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	if graph.Summary.Nodes != 34 || graph.Summary.Waves != 3 || graph.Summary.NotReady != 0 {
		t.Fatalf("unexpected summary: %+v", graph.Summary)
	}
	if len(graph.Nodes) != 34 {
		t.Fatalf("expected 34 nodes, got %d", len(graph.Nodes))
	}
	// Sakutei's /graph enumerates Waves by each node's local_id, not its
	// full id (see Memory on the aggregator bug A4's real-world data
	// exposed): every entry in every wave must be one of the nodes'
	// LocalID, never their full ID or empty.
	localIDs := make(map[string]bool, len(graph.Nodes))
	for _, n := range graph.Nodes {
		localIDs[n.LocalID] = true
	}
	if len(graph.Waves) != 3 || len(graph.Waves[0]) != 12 || len(graph.Waves[1]) != 19 || len(graph.Waves[2]) != 3 {
		t.Fatalf("unexpected wave sizes: %v", graph.Waves)
	}
	for wi, wave := range graph.Waves {
		for _, id := range wave {
			if id == "" || !localIDs[id] {
				t.Fatalf("wave %d entry %q is not a known node local_id", wi, id)
			}
		}
	}
}

func TestClient_A4Fixture_DriftedStatus(t *testing.T) {
	c := serveA4Fixture(t, "/status", readA4Fixture(t, "drifted/sakutei-status.sanitized.json"))
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Summary.Total != 34 || status.Summary.Conformant != 33 || status.Summary.Drift != 1 {
		t.Fatalf("unexpected summary: %+v", status.Summary)
	}
	var drifted []ResourceStatus
	for _, r := range status.Resources {
		if r.State == "drift" {
			drifted = append(drifted, r)
		}
	}
	if len(drifted) != 1 {
		t.Fatalf("expected exactly 1 drifted resource, got %d: %+v", len(drifted), drifted)
	}
	d := drifted[0]
	if d.Cause != "B" || d.LocalID != "server1" {
		t.Fatalf("unexpected drifted resource: %+v", d)
	}
	if len(d.Fields) != 1 || d.Fields[0].Field != "power" || d.Fields[0].From != "Off" || d.Fields[0].To != "On" {
		t.Fatalf("unexpected field diff: %+v", d.Fields)
	}
	// This is the real-world shape A5 (see Memory) fixed: Sakutei v2.6.0
	// sends tier as an object, not a string -- it must decode here without
	// failing the whole response, with its real values intact.
	if d.Tier == nil || !d.Tier.TrueObserve || d.Tier.BulkObserve || d.Tier.ProviderVersion != "redfish-system-v1" {
		t.Fatalf("unexpected tier: %+v", d.Tier)
	}
}

func TestClient_A4Fixture_DriftedPlan(t *testing.T) {
	c := serveA4Fixture(t, "/plan", readA4Fixture(t, "drifted/sakutei-plan.sanitized.json"))
	plan, err := c.Plan(context.Background())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Summary.Changes != 1 || plan.Summary.Update != 1 || plan.Summary.Blocked != 1 {
		t.Fatalf("unexpected summary: %+v", plan.Summary)
	}
	if len(plan.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(plan.Operations))
	}
	op := plan.Operations[0]
	if op.Kind != "UPDATE" || op.LocalID != "server1" || !op.Blocked || op.BlockedBy != "alert_only" {
		t.Fatalf("unexpected operation: %+v", op)
	}
	if len(op.Fields) != 1 || op.Fields[0].Field != "power" || op.Fields[0].To != "On" {
		t.Fatalf("unexpected field diff: %+v", op.Fields)
	}
}
