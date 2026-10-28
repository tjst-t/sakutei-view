// Package aggregate combines sakutei-view's three data sources — the
// containerlab topology/liveness (internal/source/containerlab), Sakutei's
// read-only resource status (internal/source/sakutei), and a device
// plugin's own resource status (internal/source/plugin) — into one
// normalized snapshot and one normalized event stream, and decides each
// node's overall color (ok/drift/apply_failed/...).
//
// Sakutei is a generic, provider-agnostic reconciler: it has no built-in
// notion of "this resource is that containerlab node". sakutei-view assumes
// the simplest possible convention to bridge the two: a Sakutei resource
// overlays a containerlab node when the resource's local_id equals the
// node's name. The device plugin contract has no such ambiguity — its
// /plugin/status resources already carry an explicit node field.
package aggregate

import (
	"context"
	"fmt"
	"time"

	"github.com/tjst-t/sakutei-view/internal/source/containerlab"
	"github.com/tjst-t/sakutei-view/internal/source/plugin"
	"github.com/tjst-t/sakutei-view/internal/source/sakutei"
)

// Color is the overall color-coding sakutei-view shows for a node.
type Color string

const (
	ColorDown         Color = "down"         // containerlab reports the container is not running
	ColorOK           Color = "ok"           // conformant / no drift
	ColorDrift        Color = "drift"        // declared and observed state differ
	ColorApplyFailed  Color = "apply_failed" // Sakutei gave up applying (oscillating + apply_stopped), or the plugin reports "error"
	ColorUnobservable Color = "unobservable" // Sakutei could not observe the resource
	ColorUnmanaged    Color = "unmanaged"    // present but not declared
	ColorWorkflow     Color = "workflow"     // mid-workflow / oscillating without a hard failure
	ColorUnknown      Color = "unknown"      // running, but no Sakutei/plugin resource overlay matched
)

// SakuteiOverlay is a Sakutei resource's status, attached to the
// containerlab node it was matched to (see package doc for the matching
// convention).
type SakuteiOverlay struct {
	ResourceID string
	// State is one of Sakutei's ResourceStatus.State values: conformant,
	// drift, unobservable, unmanaged, workflow, oscillating.
	State string
	// ApplyFailed is true when State is "oscillating" and Sakutei reports
	// it stopped retrying (ResourceStatus.ApplyStopped != nil).
	ApplyFailed   bool
	DriftedFields []string
}

// PluginOverlay is a device plugin resource's status, attached to the
// containerlab node named by its Node field.
type PluginOverlay struct {
	ResourceID string
	// State is one of the plugin contract's Resource.State values: ok,
	// drifted, unknown, error.
	State         string
	DriftedFields []string
}

// Node is one containerlab node plus whatever Sakutei/plugin overlays were
// matched to it.
type Node struct {
	containerlab.Node
	Sakutei *SakuteiOverlay
	Plugin  *PluginOverlay
}

// Color decides the node's overall color. containerlab liveness takes
// priority (a stopped container is "down" regardless of what Sakutei or the
// plugin last observed); otherwise Sakutei's resource state is preferred
// over the plugin's when both are present.
func (n Node) Color() Color {
	if n.State != "" && n.State != "running" {
		return ColorDown
	}
	if n.Sakutei != nil {
		if c, ok := colorForSakuteiState(*n.Sakutei); ok {
			return c
		}
	}
	if n.Plugin != nil {
		if c, ok := colorForPluginState(n.Plugin.State); ok {
			return c
		}
	}
	return ColorUnknown
}

func colorForSakuteiState(o SakuteiOverlay) (Color, bool) {
	if o.ApplyFailed {
		return ColorApplyFailed, true
	}
	switch o.State {
	case "conformant":
		return ColorOK, true
	case "drift":
		return ColorDrift, true
	case "unobservable":
		return ColorUnobservable, true
	case "unmanaged":
		return ColorUnmanaged, true
	case "workflow", "oscillating":
		return ColorWorkflow, true
	default:
		return "", false
	}
}

func colorForPluginState(state string) (Color, bool) {
	switch state {
	case "ok":
		return ColorOK, true
	case "drifted":
		return ColorDrift, true
	case "error":
		return ColorApplyFailed, true
	default:
		return "", false
	}
}

// Snapshot is the aggregator's combined, point-in-time view.
type Snapshot struct {
	GeneratedAt time.Time
	Nodes       []Node
	Links       []containerlab.Link
	// SakuteiSummary is Sakutei's own resource counts (GET /status's
	// "summary"), nil if no Sakutei client is configured or it has not
	// observed reality yet (sakutei.NotYet).
	SakuteiSummary *sakutei.Summary
}

// Aggregator combines the three sources. Sakutei and Plugin may be nil if
// that source is not configured for this deployment.
type Aggregator struct {
	Containerlab containerlab.Source
	Sakutei      *sakutei.Client
	Plugin       *plugin.Client
}

// New returns an Aggregator. cl must not be nil; sk and pl may be nil.
func New(cl containerlab.Source, sk *sakutei.Client, pl *plugin.Client) *Aggregator {
	return &Aggregator{Containerlab: cl, Sakutei: sk, Plugin: pl}
}

// Snapshot fetches the current topology plus whatever Sakutei/plugin status
// is configured, and merges them into one Snapshot.
func (a *Aggregator) Snapshot(ctx context.Context) (Snapshot, error) {
	topo, err := a.Containerlab.Topology(ctx)
	if err != nil {
		return Snapshot{}, fmt.Errorf("fetching containerlab topology: %w", err)
	}

	nodes := make([]Node, len(topo.Nodes))
	byName := make(map[string]int, len(topo.Nodes))
	for i, n := range topo.Nodes {
		nodes[i] = Node{Node: n}
		byName[n.Name] = i
	}

	snap := Snapshot{GeneratedAt: time.Now().UTC(), Nodes: nodes, Links: topo.Links}

	if a.Sakutei != nil {
		status, err := a.Sakutei.Status(ctx)
		switch {
		case err == nil:
			summary := status.Summary
			snap.SakuteiSummary = &summary
			for _, r := range status.Resources {
				idx, ok := byName[r.LocalID]
				if !ok {
					continue
				}
				overlay := &SakuteiOverlay{ResourceID: r.ID, State: r.State}
				for _, f := range r.Fields {
					overlay.DriftedFields = append(overlay.DriftedFields, f.Field)
				}
				if r.State == "oscillating" && r.ApplyStopped != nil {
					overlay.ApplyFailed = true
				}
				snap.Nodes[idx].Sakutei = overlay
			}
		case sakutei.NotYet(err):
			// Daemon just started; no status to overlay yet. Not an error
			// for the caller — the snapshot simply has no Sakutei overlay.
		default:
			return Snapshot{}, fmt.Errorf("fetching sakutei status: %w", err)
		}
	}

	if a.Plugin != nil {
		status, err := a.Plugin.Status(ctx)
		if err != nil {
			return Snapshot{}, fmt.Errorf("fetching plugin status: %w", err)
		}
		for _, r := range status.Resources {
			idx, ok := byName[r.Node]
			if !ok {
				continue
			}
			snap.Nodes[idx].Plugin = &PluginOverlay{
				ResourceID:    r.ID,
				State:         r.State,
				DriftedFields: r.DriftedFields,
			}
		}
	}

	return snap, nil
}
