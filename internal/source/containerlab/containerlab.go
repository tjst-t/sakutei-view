// Package containerlab provides a normalized view of a ContainerLab lab's
// topology and node state, sourced either directly from clab-api-server's
// HTTP API or indirectly through a device plugin's GET /plugin/topology
// passthrough (docs/plugin-contract.md). Both are exposed behind the same
// Source interface so the rest of sakutei-view does not need to know which
// one is configured.
package containerlab

import "context"

// Node is a single ContainerLab node (container), normalized from either
// clab-api-server's models.ClabContainerInfo or containerlab's own
// `inspect --format json` output (the two shapes overlap; see rawjson.go).
type Node struct {
	LabName string `json:"lab_name,omitempty"`
	Name    string `json:"name"`
	Kind    string `json:"kind,omitempty"` // e.g. "linux", "nokia_srlinux"
	Image   string `json:"image,omitempty"`
	State   string `json:"state,omitempty"`  // e.g. "running"
	Status  string `json:"status,omitempty"` // e.g. "Up 18 hours"
	IPv4    string `json:"ipv4,omitempty"`
	IPv6    string `json:"ipv6,omitempty"`
	Owner   string `json:"owner,omitempty"`
	Group   string `json:"group,omitempty"` // only present when the source provides it (clab-api-server); empty otherwise
}

// LinkEndpoint is one side of a Link.
type LinkEndpoint struct {
	Node      string `json:"node"`
	Interface string `json:"interface,omitempty"`
	MAC       string `json:"mac,omitempty"`
	State     string `json:"state,omitempty"`
}

// Link is a connection between two node interfaces. Only clab-api-server
// provides this; a plugin's GET /plugin/topology passthrough carries
// containerlab's own inspect output, which has no link information, so
// Topology.Links is always empty for PluginSource.
type Link struct {
	Type string       `json:"type"` // "veth" or "macvlan"
	A    LinkEndpoint `json:"a"`
	B    LinkEndpoint `json:"b"`
}

// Topology is the normalized result of fetching a lab's nodes and links.
type Topology struct {
	Nodes []Node
	Links []Link
}

// Source fetches a lab's current topology and node state. The two
// implementations (ClabAPIClient, PluginSource) are interchangeable.
type Source interface {
	Topology(ctx context.Context) (Topology, error)
}
