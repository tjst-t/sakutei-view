package containerlab

import (
	"context"
	"fmt"

	"github.com/tjst-t/sakutei-view/internal/source/plugin"
)

// PluginSource is a Source backed by a device plugin's GET /plugin/topology
// (docs/plugin-contract.md), i.e. the plugin's own passthrough of
// `containerlab inspect --format json`. It never has link information (the
// plugin contract's topology.inspect field is exactly containerlab's own
// inspect output, which has no links), so Topology().Links is always empty
// for this source.
type PluginSource struct {
	Client *plugin.Client
}

// NewPluginSource returns a PluginSource wrapping an existing plugin client.
func NewPluginSource(client *plugin.Client) *PluginSource {
	return &PluginSource{Client: client}
}

// Topology fetches GET /plugin/topology and parses its inspect field.
func (s *PluginSource) Topology(ctx context.Context) (Topology, error) {
	t, err := s.Client.Topology(ctx)
	if err != nil {
		return Topology{}, fmt.Errorf("fetching plugin topology: %w", err)
	}
	nodes, err := decodeInspectJSON(t.Inspect)
	if err != nil {
		return Topology{}, fmt.Errorf("decoding plugin topology inspect JSON: %w", err)
	}
	return Topology{Nodes: nodes}, nil
}
