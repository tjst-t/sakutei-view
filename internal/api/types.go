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

	// ActionsEnabled tells the frontend whether the write/action endpoints
	// (GET /api/scripts, POST /api/actions/{scriptID}/{action}, GET
	// /api/runs/{runID}) are available on this deployment, so it can decide
	// whether to render the operations panel without a separate
	// capability-probe request. True only when a plugin is configured AND
	// Basic auth is configured (see Server.actionsEnabled).
	ActionsEnabled bool `json:"actions_enabled"`
}

func newSnapshotResponse(snap aggregate.Snapshot, actionsEnabled bool) snapshotResponse {
	nodes := make([]nodeResponse, len(snap.Nodes))
	for i, n := range snap.Nodes {
		nodes[i] = nodeResponse{Node: n, Color: n.Color()}
	}
	return snapshotResponse{Snapshot: snap, Nodes: nodes, ActionsEnabled: actionsEnabled}
}
