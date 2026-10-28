package containerlab

import "encoding/json"

// rawContainerInfo mirrors the JSON fields shared by containerlab's own
// `inspect --format json` output (see containerlab.dev/cmd/inspect/) and
// clab-api-server's models.ClabContainerInfo (see its docs/swagger.json).
// clab-api-server's shape is a superset: it additionally has group,
// mgmt_network, nodeName and ports, none of which containerlab's own
// inspect output carries. Decoding either shape into this struct leaves the
// fields the source doesn't have at their zero value.
type rawContainerInfo struct {
	LabName     string `json:"lab_name"`
	LabPath     string `json:"labPath"`
	AbsLabPath  string `json:"absLabPath"`
	Name        string `json:"name"`
	ContainerID string `json:"container_id"`
	Image       string `json:"image"`
	Kind        string `json:"kind"`
	State       string `json:"state"`
	Status      string `json:"status"`
	IPv4Address string `json:"ipv4_address"`
	IPv6Address string `json:"ipv6_address"`
	Owner       string `json:"owner"`
	Group       string `json:"group,omitempty"`
}

func (c rawContainerInfo) toNode() Node {
	return Node{
		LabName: c.LabName,
		Name:    c.Name,
		Kind:    c.Kind,
		Image:   c.Image,
		State:   c.State,
		Status:  c.Status,
		IPv4:    c.IPv4Address,
		IPv6:    c.IPv6Address,
		Owner:   c.Owner,
		Group:   c.Group,
	}
}

// rawLinkEndpoint mirrors clab-api-server's models.LinkEndpoint.
type rawLinkEndpoint struct {
	Node      string `json:"node"`
	Interface string `json:"interface"`
	MAC       string `json:"mac"`
	State     string `json:"state"`
}

func (e rawLinkEndpoint) toEndpoint() LinkEndpoint {
	return LinkEndpoint{Node: e.Node, Interface: e.Interface, MAC: e.MAC, State: e.State}
}

// rawLink mirrors clab-api-server's models.LabLink.
type rawLink struct {
	A    rawLinkEndpoint `json:"a"`
	B    rawLinkEndpoint `json:"b"`
	Type string          `json:"type"`
}

func (l rawLink) toLink() Link {
	return Link{Type: l.Type, A: l.A.toEndpoint(), B: l.B.toEndpoint()}
}

// decodeInspectJSON parses containerlab's own `inspect --format json` shape:
// an object keyed by lab name, whose value is an array of container
// objects. This is also exactly the shape of the "inspect" field in a
// device plugin's GET /plugin/topology response, and of clab-api-server's
// GET /api/v1/labs (models.ClabInspectOutput).
func decodeInspectJSON(data []byte) ([]Node, error) {
	var byLab map[string][]rawContainerInfo
	if err := json.Unmarshal(data, &byLab); err != nil {
		return nil, err
	}
	var nodes []Node
	for _, containers := range byLab {
		for _, c := range containers {
			nodes = append(nodes, c.toNode())
		}
	}
	return nodes, nil
}
