package containerlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// clabAPINodesFixture mirrors the real shape of GET /api/v1/labs/{labName}
// from clab-api-server: a bare JSON array of models.ClabContainerInfo
// (confirmed against the project's own docs/swagger.json).
const clabAPINodesFixture = `[
  {
    "absLabPath": "/home/user/mylab.clab.yml",
    "container_id": "abcd1234",
    "group": "leaf",
    "image": "ghcr.io/nokia/srlinux",
    "ipv4_address": "172.20.20.2/24",
    "ipv6_address": "",
    "kind": "nokia_srlinux",
    "labPath": "mylab.clab.yml",
    "lab_name": "mylab",
    "mgmt_network": "clab",
    "name": "clab-mylab-leaf1",
    "nodeName": "leaf1",
    "owner": "user",
    "ports": [],
    "state": "running",
    "status": "Up 2 hours"
  },
  {
    "absLabPath": "/home/user/mylab.clab.yml",
    "container_id": "ef567890",
    "group": "spine",
    "image": "ghcr.io/nokia/srlinux",
    "ipv4_address": "172.20.20.3/24",
    "ipv6_address": "",
    "kind": "nokia_srlinux",
    "labPath": "mylab.clab.yml",
    "lab_name": "mylab",
    "mgmt_network": "clab",
    "name": "clab-mylab-spine1",
    "nodeName": "spine1",
    "owner": "user",
    "ports": [],
    "state": "exited",
    "status": "Exited (1) 3 minutes ago"
  }
]`

// clabAPILinksFixture mirrors GET /api/v1/labs/{labName}/links: a bare JSON
// array of models.LabLink.
const clabAPILinksFixture = `[
  {
    "a": {
      "alias": "ethernet-1/1",
      "container": "clab-mylab-leaf1",
      "ifindex": 1,
      "interface": "e1-1",
      "mac": "aa:bb:cc:dd:ee:01",
      "master": "",
      "mtu": 1500,
      "namespace": "node",
      "node": "leaf1",
      "state": "up"
    },
    "b": {
      "alias": "ethernet-1/1",
      "container": "clab-mylab-spine1",
      "ifindex": 1,
      "interface": "e1-1",
      "mac": "aa:bb:cc:dd:ee:02",
      "master": "",
      "mtu": 1500,
      "namespace": "node",
      "node": "spine1",
      "state": "up"
    },
    "type": "veth"
  }
]`

func TestClabAPIClient_Topology(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/labs/mylab", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(clabAPINodesFixture))
	})
	mux.HandleFunc("GET /api/v1/labs/mylab/links", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(clabAPILinksFixture))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClabAPIClient(srv.URL, "mylab", nil)
	topo, err := client.Topology(context.Background())
	if err != nil {
		t.Fatalf("Topology: %v", err)
	}

	if len(topo.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2", len(topo.Nodes))
	}
	n0 := topo.Nodes[0]
	if n0.Name != "clab-mylab-leaf1" || n0.Kind != "nokia_srlinux" || n0.State != "running" ||
		n0.IPv4 != "172.20.20.2/24" || n0.Owner != "user" || n0.Group != "leaf" || n0.LabName != "mylab" {
		t.Errorf("Nodes[0] = %+v, unexpected", n0)
	}
	n1 := topo.Nodes[1]
	if n1.Name != "clab-mylab-spine1" || n1.State != "exited" || n1.Group != "spine" {
		t.Errorf("Nodes[1] = %+v, unexpected", n1)
	}

	if len(topo.Links) != 1 {
		t.Fatalf("Links = %d, want 1", len(topo.Links))
	}
	l0 := topo.Links[0]
	if l0.Type != "veth" || l0.A.Node != "leaf1" || l0.A.Interface != "e1-1" || l0.A.MAC != "aa:bb:cc:dd:ee:01" ||
		l0.B.Node != "spine1" || l0.B.State != "up" {
		t.Errorf("Links[0] = %+v, unexpected", l0)
	}
}

func TestClabAPIClient_Topology_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/labs/missing", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"Lab not found"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClabAPIClient(srv.URL, "missing", nil)
	_, err := client.Topology(context.Background())
	if err == nil {
		t.Fatal("Topology: want error, got nil")
	}
}
