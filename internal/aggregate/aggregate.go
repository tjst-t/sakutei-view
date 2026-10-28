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
//
// # Resilience
//
// Each source is polled with its own per-request deadline (RequestTimeout),
// so a source that stops responding cannot block a Snapshot call forever.
// If a poll fails (timeout, connection refused, non-2xx, ...), Snapshot
// does not fail outright: it falls back to the last value that source
// returned successfully (if any), and records the failure in
// Snapshot.Health instead. This means one source being down never prevents
// the other two from being shown. Health distinguishes a source that was
// never configured for this deployment from one that is configured but
// currently unreachable.
package aggregate

import (
	"context"
	"sync"
	"time"

	"github.com/tjst-t/sakutei-view/internal/source/containerlab"
	"github.com/tjst-t/sakutei-view/internal/source/plugin"
	"github.com/tjst-t/sakutei-view/internal/source/sakutei"
)

// Defaults used when the corresponding Aggregator field is zero.
const (
	// DefaultRequestTimeout bounds each individual poll request
	// (containerlab topology, Sakutei status, plugin status) when
	// Aggregator.RequestTimeout is not set.
	DefaultRequestTimeout = 10 * time.Second

	// DefaultReconnectInitial is the first wait before retrying a dropped
	// SSE stream, when Aggregator.ReconnectInitial is not set.
	DefaultReconnectInitial = 1 * time.Second

	// DefaultReconnectMax caps the exponential backoff between SSE
	// reconnect attempts, when Aggregator.ReconnectMax is not set.
	DefaultReconnectMax = 30 * time.Second
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

// SourceName identifies one of the three data sources, for Snapshot.Health.
type SourceName string

const (
	SourceContainerlab SourceName = "containerlab"
	SourceSakutei      SourceName = "sakutei"
	SourcePlugin       SourceName = "plugin"
)

// SourceHealth is one source's reachability, as of the most recent attempt
// to read it (via Snapshot's polling or Events' SSE subscription,
// whichever happened most recently).
type SourceHealth struct {
	// Configured is false when this deployment has no client for this
	// source at all (e.g. no SAKUTEI_BASE_URL set). A source that is not
	// Configured is never shown as "unreachable" — it was simply not asked
	// for.
	Configured bool `json:"configured"`
	// OK is true when the most recent attempt succeeded (or, for Sakutei,
	// when the daemon answered but has not completed its first cycle yet —
	// that is a normal startup state, not a failure). Meaningless when
	// Configured is false.
	OK bool `json:"ok"`
	// LastSuccess is when a request to this source last succeeded. Nil if
	// it never has.
	LastSuccess *time.Time `json:"last_success,omitempty"`
	// LastError is the most recent failure's message. Empty when OK is
	// true or no attempt has been made yet.
	LastError string `json:"last_error,omitempty"`
}

// SakuteiOverlay is a Sakutei resource's status, attached to the
// containerlab node it was matched to (see package doc for the matching
// convention).
type SakuteiOverlay struct {
	ResourceID string `json:"resource_id"`
	// State is one of Sakutei's ResourceStatus.State values: conformant,
	// drift, unobservable, unmanaged, workflow, oscillating.
	State string `json:"state"`
	// ApplyFailed is true when State is "oscillating" and Sakutei reports
	// it stopped retrying (ResourceStatus.ApplyStopped != nil).
	ApplyFailed   bool     `json:"apply_failed,omitempty"`
	DriftedFields []string `json:"drifted_fields,omitempty"`
}

// PluginOverlay is a device plugin resource's status, attached to the
// containerlab node named by its Node field.
type PluginOverlay struct {
	ResourceID string `json:"resource_id"`
	// State is one of the plugin contract's Resource.State values: ok,
	// drifted, unknown, error.
	State         string   `json:"state"`
	DriftedFields []string `json:"drifted_fields,omitempty"`
}

// Node is one containerlab node plus whatever Sakutei/plugin overlays were
// matched to it.
type Node struct {
	containerlab.Node
	Sakutei *SakuteiOverlay `json:"sakutei,omitempty"`
	Plugin  *PluginOverlay  `json:"plugin,omitempty"`
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
	GeneratedAt time.Time           `json:"generated_at"`
	Nodes       []Node              `json:"nodes"`
	Links       []containerlab.Link `json:"links"`
	// SakuteiSummary is Sakutei's own resource counts (GET /status's
	// "summary"), nil if no Sakutei client is configured, it has not
	// observed reality yet (sakutei.NotYet), or it is currently
	// unreachable and no prior successful read exists to fall back to.
	SakuteiSummary *sakutei.Summary `json:"sakutei_summary,omitempty"`
	// Health reports, per source, whether it is configured and whether the
	// most recent read succeeded. See SourceHealth.
	Health map[SourceName]SourceHealth `json:"health"`
}

// Aggregator combines the three sources. Sakutei and Plugin may be nil if
// that source is not configured for this deployment.
type Aggregator struct {
	Containerlab containerlab.Source
	Sakutei      *sakutei.Client
	Plugin       *plugin.Client

	// RequestTimeout bounds each individual poll request this Aggregator
	// makes (containerlab topology, Sakutei status, plugin status). Zero
	// means DefaultRequestTimeout.
	RequestTimeout time.Duration

	// ReconnectInitial and ReconnectMax bound the exponential backoff used
	// to reconnect a dropped SSE stream (Sakutei /events, plugin
	// /plugin/events). Zero means DefaultReconnectInitial/DefaultReconnectMax.
	ReconnectInitial time.Duration
	ReconnectMax     time.Duration

	mu             sync.Mutex
	health         map[SourceName]SourceHealth
	haveTopology   bool
	cachedTopology containerlab.Topology
	cachedSakutei  *sakutei.StatusResponse
	cachedPlugin   *plugin.StatusResponse
}

// New returns an Aggregator. cl must not be nil; sk and pl may be nil.
func New(cl containerlab.Source, sk *sakutei.Client, pl *plugin.Client) *Aggregator {
	return &Aggregator{
		Containerlab: cl,
		Sakutei:      sk,
		Plugin:       pl,
		health: map[SourceName]SourceHealth{
			SourceContainerlab: {Configured: cl != nil},
			SourceSakutei:      {Configured: sk != nil},
			SourcePlugin:       {Configured: pl != nil},
		},
	}
}

func (a *Aggregator) requestTimeout() time.Duration {
	if a.RequestTimeout > 0 {
		return a.RequestTimeout
	}
	return DefaultRequestTimeout
}

func (a *Aggregator) reconnectInitial() time.Duration {
	if a.ReconnectInitial > 0 {
		return a.ReconnectInitial
	}
	return DefaultReconnectInitial
}

func (a *Aggregator) reconnectMax() time.Duration {
	if a.ReconnectMax > 0 {
		return a.ReconnectMax
	}
	return DefaultReconnectMax
}

func (a *Aggregator) recordSuccess(name SourceName) {
	now := time.Now().UTC()
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health[name]
	h.Configured = true
	h.OK = true
	h.LastSuccess = &now
	h.LastError = ""
	a.health[name] = h
}

func (a *Aggregator) recordError(name SourceName, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := a.health[name]
	h.Configured = true
	h.OK = false
	h.LastError = err.Error()
	a.health[name] = h
}

// healthSnapshot returns a copy of the current health map, safe to embed in
// a Snapshot returned to a caller.
func (a *Aggregator) healthSnapshot() map[SourceName]SourceHealth {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[SourceName]SourceHealth, len(a.health))
	for k, v := range a.health {
		out[k] = v
	}
	return out
}

// fetchTopology polls the containerlab source with a bounded deadline. On
// failure it returns the last successfully-fetched topology (or a zero
// Topology if none has ever succeeded) and records the failure in Health;
// it does not return an error to the caller.
func (a *Aggregator) fetchTopology(ctx context.Context) containerlab.Topology {
	reqCtx, cancel := context.WithTimeout(ctx, a.requestTimeout())
	defer cancel()

	topo, err := a.Containerlab.Topology(reqCtx)
	if err != nil {
		a.recordError(SourceContainerlab, err)
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.haveTopology {
			return a.cachedTopology
		}
		return containerlab.Topology{}
	}

	a.recordSuccess(SourceContainerlab)
	a.mu.Lock()
	a.cachedTopology = topo
	a.haveTopology = true
	a.mu.Unlock()
	return topo
}

// fetchSakuteiStatus polls Sakutei's /status with a bounded deadline.
// Returns nil if Sakutei is not configured, hasn't observed reality yet, or
// is unreachable with no prior successful read to fall back to.
func (a *Aggregator) fetchSakuteiStatus(ctx context.Context) *sakutei.StatusResponse {
	if a.Sakutei == nil {
		return nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, a.requestTimeout())
	defer cancel()

	status, err := a.Sakutei.Status(reqCtx)
	switch {
	case err == nil:
		a.recordSuccess(SourceSakutei)
		cp := status
		a.mu.Lock()
		a.cachedSakutei = &cp
		a.mu.Unlock()
		return &status
	case sakutei.NotYet(err):
		// Daemon just started and answered, but has not finished its first
		// cycle; this is a normal startup state, not a failure.
		a.recordSuccess(SourceSakutei)
		return nil
	default:
		a.recordError(SourceSakutei, err)
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.cachedSakutei
	}
}

// fetchPluginStatus polls the plugin's /plugin/status with a bounded
// deadline. Returns nil if the plugin is not configured or is unreachable
// with no prior successful read to fall back to.
func (a *Aggregator) fetchPluginStatus(ctx context.Context) *plugin.StatusResponse {
	if a.Plugin == nil {
		return nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, a.requestTimeout())
	defer cancel()

	status, err := a.Plugin.Status(reqCtx)
	if err != nil {
		a.recordError(SourcePlugin, err)
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.cachedPlugin
	}

	a.recordSuccess(SourcePlugin)
	cp := status
	a.mu.Lock()
	a.cachedPlugin = &cp
	a.mu.Unlock()
	return &status
}

// Snapshot fetches the current topology plus whatever Sakutei/plugin status
// is configured, and merges them into one Snapshot. Each source is given
// its own bounded deadline (RequestTimeout); a source that times out or
// errors does not fail the whole Snapshot — the last successfully-fetched
// value for that source is used instead (or it is simply absent, if none
// has ever succeeded), and the failure is recorded in Snapshot.Health.
//
// Snapshot only returns a non-nil error when ctx itself is already done
// when called.
func (a *Aggregator) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}

	topo := a.fetchTopology(ctx)
	sakuteiStatus := a.fetchSakuteiStatus(ctx)
	pluginStatus := a.fetchPluginStatus(ctx)

	nodes := make([]Node, len(topo.Nodes))
	byName := make(map[string]int, len(topo.Nodes))
	for i, n := range topo.Nodes {
		nodes[i] = Node{Node: n}
		byName[n.Name] = i
	}

	snap := Snapshot{
		GeneratedAt: time.Now().UTC(),
		Nodes:       nodes,
		Links:       topo.Links,
		Health:      a.healthSnapshot(),
	}

	if sakuteiStatus != nil {
		summary := sakuteiStatus.Summary
		snap.SakuteiSummary = &summary
		for _, r := range sakuteiStatus.Resources {
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
	}

	if pluginStatus != nil {
		for _, r := range pluginStatus.Resources {
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
