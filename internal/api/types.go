package api

import "github.com/tjst-t/sakutei-view/internal/aggregate"

// nodeResponse is aggregate.Node plus its computed Color, which the
// aggregate package exposes only as a method (Node.Color()) rather than a
// stored field.
type nodeResponse struct {
	aggregate.Node
	Color aggregate.Color `json:"color"`
}

// snapshotResponse is the exact JSON shape of GET /api/snapshot.
type snapshotResponse struct {
	aggregate.Snapshot
	Nodes []nodeResponse `json:"nodes"`
}

func newSnapshotResponse(snap aggregate.Snapshot) snapshotResponse {
	nodes := make([]nodeResponse, len(snap.Nodes))
	for i, n := range snap.Nodes {
		nodes[i] = nodeResponse{Node: n, Color: n.Color()}
	}
	return snapshotResponse{Snapshot: snap, Nodes: nodes}
}
