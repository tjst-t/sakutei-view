package containerlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tjst-t/sakutei-view/internal/source/plugin"
)

// pluginTopologyFixture mirrors GET /plugin/topology's response, whose
// "inspect" field is exactly containerlab's own `inspect --format json`
// output: an object keyed by lab name, value an array of container objects
// (confirmed against containerlab.dev/cmd/inspect/). Note the absence of
// group/mgmt_network/nodeName/ports here, unlike clab-api-server's shape.
const pluginTopologyFixture = `{
  "inspect": {
    "mylab": [
      {
        "lab_name": "mylab",
        "labPath": "mylab.clab.yml",
        "absLabPath": "/home/user/mylab.clab.yml",
        "name": "clab-mylab-node1",
        "container_id": "abcd1234",
        "image": "ghcr.io/nokia/srlinux",
        "kind": "nokia_srlinux",
        "state": "running",
        "status": "Up 2 hours",
        "ipv4_address": "172.20.20.2/24",
        "ipv6_address": "",
        "owner": "user"
      }
    ]
  },
  "clab_yaml": "name: mylab\ntopology:\n  nodes:\n    node1:\n      kind: nokia_srlinux\n"
}`

func TestPluginSource_Topology(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /plugin/topology", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(pluginTopologyFixture))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := NewPluginSource(plugin.NewClient(srv.URL, nil))
	topo, err := src.Topology(context.Background())
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}

	if len(topo.Links) != 0 {
		t.Errorf("Links = %d, want 0 (plugin topology carries no link info)", len(topo.Links))
	}
	if len(topo.Nodes) != 1 {
		t.Fatalf("Nodes = %d, want 1", len(topo.Nodes))
	}
	n := topo.Nodes[0]
	if n.Name != "clab-mylab-node1" || n.Kind != "nokia_srlinux" || n.State != "running" ||
		n.IPv4 != "172.20.20.2/24" || n.Owner != "user" || n.LabName != "mylab" || n.Group != "" {
		t.Errorf("Nodes[0] = %+v, unexpected", n)
	}
}
