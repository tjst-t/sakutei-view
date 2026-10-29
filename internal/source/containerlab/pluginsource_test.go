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
// clab_yaml's topology.links is this source's only way to get link data
// (the inspect output above carries none) -- see pluginsource.go.
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
      },
      {
        "lab_name": "mylab",
        "labPath": "mylab.clab.yml",
        "absLabPath": "/home/user/mylab.clab.yml",
        "name": "clab-mylab-node2",
        "container_id": "ef567890",
        "image": "ghcr.io/nokia/srlinux",
        "kind": "nokia_srlinux",
        "state": "running",
        "status": "Up 2 hours",
        "ipv4_address": "172.20.20.3/24",
        "ipv6_address": "",
        "owner": "user"
      }
    ]
  },
  "clab_yaml": "name: mylab\ntopology:\n  nodes:\n    node1:\n      kind: nokia_srlinux\n    node2:\n      kind: nokia_srlinux\n  links:\n    - endpoints: [\"node1:e1-1\", \"node2:e1-1\"]\n"
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

	if len(topo.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2", len(topo.Nodes))
	}
	n := topo.Nodes[0]
	if n.Name != "node1" || n.ContainerName != "clab-mylab-node1" || n.Kind != "nokia_srlinux" || n.State != "running" ||
		n.IPv4 != "172.20.20.2/24" || n.Owner != "user" || n.LabName != "mylab" || n.Group != "" {
		t.Errorf("Nodes[0] = %+v, unexpected", n)
	}

	if len(topo.Links) != 1 {
		t.Fatalf("Links = %d, want 1 (from clab_yaml's topology.links)", len(topo.Links))
	}
	l0 := topo.Links[0]
	if l0.Type != "veth" || l0.A.Node != "node1" || l0.A.Interface != "e1-1" ||
		l0.B.Node != "node2" || l0.B.Interface != "e1-1" {
		t.Errorf("Links[0] = %+v, unexpected", l0)
	}
}
