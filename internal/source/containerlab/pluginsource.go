package containerlab

import (
	"context"
	"fmt"

	"github.com/tjst-t/sakutei-view/internal/source/plugin"
)

// PluginSource is a Source backed by a device plugin's GET /plugin/topology
// (docs/plugin-contract.md): node state comes from the plugin's inspect
// passthrough (exactly containerlab's own `inspect --format json` output,
// which has no link information), and link information comes separately
// from the plugin's clab_yaml passthrough (the lab's generated .clab.yml,
// whose topology.links section is the only place link data exists on this
// path -- see clabyaml.go).
type PluginSource struct {
	Client *plugin.Client
}

// NewPluginSource returns a PluginSource wrapping an existing plugin client.
func NewPluginSource(client *plugin.Client) *PluginSource {
	return &PluginSource{Client: client}
}

// Topology fetches GET /plugin/topology and parses its inspect and
// clab_yaml fields. clab_yaml is optional (older plugins, or ones that
// simply don't set it, leave it empty); when absent, Topology().Links is
// empty and the nodes are still returned, same as before link parsing
// existed.
func (s *PluginSource) Topology(ctx context.Context) (Topology, error) {
	t, err := s.Client.Topology(ctx)
	if err != nil {
		return Topology{}, fmt.Errorf("fetching plugin topology: %w", err)
	}
	yamlLabName, links, err := parseClabYAMLLinks(t.ClabYAML)
	if err != nil {
		return Topology{}, fmt.Errorf("parsing plugin topology clab_yaml: %w", err)
	}
	nodes, err := decodeInspectJSON(t.Inspect)
	if err != nil {
		return Topology{}, fmt.Errorf("decoding plugin topology inspect JSON: %w", err)
	}
	if yamlLabName != "" {
		// Last-resort lab-name fallback: decodeInspectJSON already prefers
		// each container's own lab_name, then the inspect JSON's outer map
		// key. Only fill in nodes that still have neither (and so never
		// got a chance to have their container-name prefix stripped).
		for i := range nodes {
			if nodes[i].LabName == "" {
				nodes[i].LabName = yamlLabName
				nodes[i].Name = shortNodeName(nodes[i].ContainerName, yamlLabName)
			}
		}
	}
	return Topology{Nodes: nodes, Links: links}, nil
}
