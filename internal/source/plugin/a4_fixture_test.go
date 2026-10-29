package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A4 fixtures: sanitized, real-world device-plugin /plugin/status and
// /plugin/topology responses collected from an actual deployment (one
// "converged" scenario, one "drifted" scenario), scrubbed of anything
// internal before being checked in under ../../../testdata. These pin
// this package's decode path against a real plugin's output, including
// fields (clab_yaml content, sakutei_bindings) a synthetic fixture might
// not think to include.
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

func findResource(resources []Resource, id string) (Resource, bool) {
	for _, r := range resources {
		if r.ID == id {
			return r, true
		}
	}
	return Resource{}, false
}

func TestClient_A4Fixture_ConvergedStatus(t *testing.T) {
	c := serveA4Fixture(t, "/plugin/status", readA4Fixture(t, "converged/plugin-status.sanitized.json"))
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(status.Resources) == 0 {
		t.Fatal("expected a non-empty resources list")
	}
	for _, r := range status.Resources {
		if r.State != "ok" {
			t.Fatalf("expected every resource ok in the converged fixture, got %q for %s", r.State, r.ID)
		}
	}
	server1, ok := findResource(status.Resources, "server:server1")
	if !ok {
		t.Fatal("expected a server:server1 resource")
	}
	if server1.Fields["power"] != "on" {
		t.Fatalf("expected server1 power=on in the converged fixture, got: %+v", server1.Fields)
	}
	if len(status.SakuteiBindings) != 30 {
		t.Fatalf("expected 30 sakutei_bindings, got %d", len(status.SakuteiBindings))
	}
}

func TestClient_A4Fixture_ConvergedTopology(t *testing.T) {
	c := serveA4Fixture(t, "/plugin/topology", readA4Fixture(t, "converged/plugin-topology.sanitized.json"))
	topo, err := c.Topology(context.Background())
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}
	if len(topo.Inspect) == 0 {
		t.Fatal("expected a non-empty inspect payload")
	}
	if topo.ClabYAML == "" {
		t.Fatal("expected a non-empty clab_yaml payload")
	}
	// The real .clab.yml this fixture carries includes several <redacted>
	// placeholders for secret env vars (see Memory: this package has no
	// opinion on clab_yaml's content, it is an opaque string) -- just make
	// sure the un-escaping done when this fixture was written back from its
	// delivery actually produced literal "<redacted>", not an HTML entity.
	if !containsLiteral(topo.ClabYAML, "<redacted>") {
		t.Fatal("expected the clab_yaml to contain a literal <redacted> placeholder")
	}
}

func containsLiteral(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestClient_A4Fixture_DriftedStatus(t *testing.T) {
	c := serveA4Fixture(t, "/plugin/status", readA4Fixture(t, "drifted/plugin-status.sanitized.json"))
	status, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	var drifted []Resource
	for _, r := range status.Resources {
		if r.State == "drifted" {
			drifted = append(drifted, r)
		}
	}
	if len(drifted) != 1 {
		t.Fatalf("expected exactly 1 drifted resource, got %d: %+v", len(drifted), drifted)
	}
	d := drifted[0]
	if d.ID != "server:server1" || len(d.DriftedFields) != 1 || d.DriftedFields[0] != "power" {
		t.Fatalf("unexpected drifted resource: %+v", d)
	}
	if d.Fields["power"] != "off" || d.Fields["want.power"] != "on" {
		t.Fatalf("unexpected drifted fields: %+v", d.Fields)
	}
}
