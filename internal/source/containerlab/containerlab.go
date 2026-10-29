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
//
// Name is always the node's name *within the topology* (what a .clab.yml
// calls it, and what a device plugin's /plugin/status resources[].node and
// Sakutei's local_id both identify it by) -- never the underlying Docker
// container's name, which containerlab derives by prefixing it with
// "clab-<lab-name>-". sakutei-view matches overlays onto nodes by this
// field, so getting it right (not the container name) is what makes the
// node-matching/coloring work at all. See ContainerName for the raw
// container name, kept only for display/debugging.
type Node struct {
	LabName string `json:"lab_name,omitempty"`
	Name    string `json:"name"`
	// ContainerName is the underlying Docker container's name (containerlab's
	// own "clab-<lab-name>-<node-name>" convention), as opposed to Name's
	// topology-level node name. Empty if a source never had a distinct
	// container name to begin with.
	ContainerName string `json:"container_name,omitempty"`
	Kind          string `json:"kind,omitempty"` // e.g. "linux", "nokia_srlinux"
	Image         string `json:"image,omitempty"`
	State         string `json:"state,omitempty"`  // e.g. "running"
	Status        string `json:"status,omitempty"` // e.g. "Up 18 hours"
	IPv4          string `json:"ipv4,omitempty"`
	IPv6          string `json:"ipv6,omitempty"`
	Owner         string `json:"owner,omitempty"`
	Group         string `json:"group,omitempty"` // only present when the source provides it (clab-api-server); empty otherwise
}

// LinkEndpoint is one side of a Link.
type LinkEndpoint struct {
	Node      string `json:"node"`
	Interface string `json:"interface,omitempty"`
	MAC       string `json:"mac,omitempty"`
	State     string `json:"state,omitempty"`
}

// Link is a connection between two node interfaces, identified by
// LinkEndpoint.Node (a topology node name, same convention as Node.Name).
// clab-api-server reports this directly; PluginSource derives it instead
// from the generated .clab.yml's topology.links section (its inspect
// passthrough alone carries no link information -- see pluginsource.go).
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
