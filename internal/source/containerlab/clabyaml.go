package containerlab

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// clabYAMLDoc is the minimal shape sakutei-view needs out of a generated
// .clab.yml file: the lab's declared name (the "name:" top-level field,
// which containerlab prefixes onto each node to get its container name --
// see shortNodeName) and its topology.links section (used to draw the
// topology diagram's edges when the containerlab data source has no other
// way to report them -- see PluginSource.Topology).
//
// Deliberately decodes only these two things. A .clab.yml has many more
// fields (node definitions, startup configs, per-kind settings, ...) that
// sakutei-view has no use for and does not attempt to parse.
type clabYAMLDoc struct {
	Name     string `yaml:"name"`
	Topology struct {
		Links []struct {
			// Type is containerlab's extended link-format discriminator
			// ("veth", "macvlan", "mgmt-net", "host", "dummy", "vxlan",
			// ...). Classic short-hand entries (just two endpoints, no
			// type:) are always a veth between two container interfaces,
			// so an empty Type defaults to "veth" in parseClabYAMLLinks.
			Type string `yaml:"type"`
			// Endpoints is a pair of "<node>:<interface>" strings -- the
			// shape containerlab itself documents and generates. Other
			// link forms some .clab.yml files use (e.g. a single
			// "endpoint: ..." plus "mac-vlan-interface" for macvlan links)
			// are not represented here and are simply skipped by
			// parseClabYAMLLinks, same as any other entry it cannot parse.
			Endpoints []string `yaml:"endpoints"`
		} `yaml:"links"`
	} `yaml:"topology"`
}

// parseClabYAMLLinks parses a .clab.yml file's topology.links section into
// Links, and returns the lab's declared name (its "name:" top-level field)
// alongside, for use as a last-resort fallback lab name (see
// PluginSource.Topology). data being empty returns ("", nil, nil) -- not an
// error -- so a plugin that doesn't set clab_yaml simply reports no links,
// same as before this parsing existed.
func parseClabYAMLLinks(data string) (labName string, links []Link, err error) {
	if strings.TrimSpace(data) == "" {
		return "", nil, nil
	}
	var doc clabYAMLDoc
	if err := yaml.Unmarshal([]byte(data), &doc); err != nil {
		return "", nil, fmt.Errorf("parsing clab_yaml: %w", err)
	}
	for _, l := range doc.Topology.Links {
		if len(l.Endpoints) != 2 {
			continue
		}
		a, aOK := splitLinkEndpoint(l.Endpoints[0])
		b, bOK := splitLinkEndpoint(l.Endpoints[1])
		if !aOK || !bOK {
			continue
		}
		linkType := l.Type
		if linkType == "" {
			linkType = "veth"
		}
		links = append(links, Link{Type: linkType, A: a, B: b})
	}
	return doc.Name, links, nil
}

// splitLinkEndpoint parses one "<node>:<interface>" endpoint string.
func splitLinkEndpoint(s string) (LinkEndpoint, bool) {
	node, iface, found := strings.Cut(s, ":")
	if !found || node == "" {
		return LinkEndpoint{}, false
	}
	return LinkEndpoint{Node: node, Interface: iface}, true
}
