// Package aggregate combines sakutei-view's three data sources — the
// containerlab topology/liveness (internal/source/containerlab), Sakutei's
// read-only resource status (internal/source/sakutei), and a device
// plugin's own resource status (internal/source/plugin) — into one
// normalized snapshot and one normalized event stream, and decides each
// node's overall color (ok/drift/apply_failed/...).
//
// Sakutei is a generic, provider-agnostic reconciler: it has no built-in
// notion of "this resource is that containerlab node". sakutei-view bridges
// the two with a generic convention plus an explicit override:
//
//   - Generic: a Sakutei resource overlays the containerlab node named by
//     the first "/"-delimited segment of its local_id (e.g. local_id
//     "fw1/inet/filter/forward" overlays node "fw1"), or by the whole
//     local_id when it has no "/" at all. See sakuteiNodeNameFromLocalID.
//   - Explicit: a device plugin may optionally report a resource-id-to-node
//     mapping via GET /plugin/status's sakutei_bindings field (contract
//     v0.3). When present for a given resource id, it wins over the
//     generic rule — this is how a resource whose local_id doesn't follow
//     the node-prefix convention (or needs to bind to a different node
//     than a literal prefix match would find) still gets placed correctly.
//     See explicitSakuteiBindings.
//
// A resource matching no node by either rule (e.g. an id scoped to
// something that isn't a containerlab node at all, like an external
// account or a DNS zone) is simply not shown on the topology diagram — see
// the Sakutei summary/dependency views for those instead.
//
// The device plugin contract has no such ambiguity for its own resources —
// /plugin/status's entries already carry an explicit node field.
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
	"sort"
	"strings"
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

// StopReason is one reason some part of Sakutei's reconcile cycle is
// currently halted: either a single resource that Sakutei gave up retrying
// (its own apply_stopped reason), a cycle-wide gate (the budget or
// capacity limiter tripping), or a provider's prune being held back.
type StopReason struct {
	// Scope is "resource", "budget", "capacity" or "prune".
	Scope string `json:"scope"`
	// Target is the resource id (scope=resource) or provider name
	// (scope=prune); empty for the cycle-wide budget/capacity gates.
	Target string `json:"target,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// GraphNode is one resource's position in Sakutei's dependency graph (GET
// /graph), grouped under SakuteiGraph.Waves.
type GraphNode struct {
	ID         string `json:"id"`
	Wave       int    `json:"wave"`
	Ready      bool   `json:"ready"`
	WaitingFor string `json:"waiting_for,omitempty"`
	External   bool   `json:"external,omitempty"`
}

// SakuteiGraph is Sakutei's dependency graph (GET /graph), with resources
// grouped into the waves they become ready in: Waves[i] lists the
// resources computed in Sakutei's i-th wave (see Sakutei's docs on
// dependency ordering). A resource with Ready=false and WaitingFor set is
// blocked on another resource in an earlier wave.
type SakuteiGraph struct {
	Waves [][]GraphNode `json:"waves"`
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
	// SakuteiStopReasons lists every reason (from GET /status and GET
	// /plan) something in Sakutei's reconcile cycle is currently halted.
	// Empty (nil) when nothing is halted, or Sakutei/its plan is
	// unavailable.
	SakuteiStopReasons []StopReason `json:"sakutei_stop_reasons,omitempty"`
	// SakuteiGraph is Sakutei's dependency graph (GET /graph), nil if no
	// Sakutei client is configured or it has never answered successfully.
	SakuteiGraph *SakuteiGraph `json:"sakutei_graph,omitempty"`
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

	mu                 sync.Mutex
	health             map[SourceName]SourceHealth
	haveTopology       bool
	cachedTopology     containerlab.Topology
	cachedSakutei      *sakutei.StatusResponse
	cachedSakuteiPlan  *sakutei.PlanResponse
	cachedSakuteiGraph *sakutei.GraphResponse
	cachedPlugin       *plugin.StatusResponse
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

// fetchSakuteiPlan polls Sakutei's /plan with a bounded deadline. Returns
// nil if Sakutei is not configured, hasn't observed reality yet, or is
// unreachable with no prior successful read to fall back to.
//
// This shares SourceSakutei's health entry with fetchSakuteiStatus and
// fetchSakuteiGraph (all three read the same daemon); when they disagree
// within one Snapshot call, the last one polled wins. In practice they
// either all succeed or all fail together, since they hit the same
// read-only listener.
func (a *Aggregator) fetchSakuteiPlan(ctx context.Context) *sakutei.PlanResponse {
	if a.Sakutei == nil {
		return nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, a.requestTimeout())
	defer cancel()

	plan, err := a.Sakutei.Plan(reqCtx)
	switch {
	case err == nil:
		a.recordSuccess(SourceSakutei)
		cp := plan
		a.mu.Lock()
		a.cachedSakuteiPlan = &cp
		a.mu.Unlock()
		return &plan
	case sakutei.NotYet(err):
		a.recordSuccess(SourceSakutei)
		return nil
	default:
		a.recordError(SourceSakutei, err)
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.cachedSakuteiPlan
	}
}

// fetchSakuteiGraph polls Sakutei's /graph with a bounded deadline. Returns
// nil if Sakutei is not configured, hasn't observed reality yet, or is
// unreachable with no prior successful read to fall back to.
func (a *Aggregator) fetchSakuteiGraph(ctx context.Context) *sakutei.GraphResponse {
	if a.Sakutei == nil {
		return nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, a.requestTimeout())
	defer cancel()

	graph, err := a.Sakutei.Graph(reqCtx)
	switch {
	case err == nil:
		a.recordSuccess(SourceSakutei)
		cp := graph
		a.mu.Lock()
		a.cachedSakuteiGraph = &cp
		a.mu.Unlock()
		return &graph
	case sakutei.NotYet(err):
		a.recordSuccess(SourceSakutei)
		return nil
	default:
		a.recordError(SourceSakutei, err)
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.cachedSakuteiGraph
	}
}

// buildStopReasons collects every reason (from /status's per-resource
// apply_stopped, and /plan's budget/capacity gates and prune halts) that
// part of Sakutei's reconcile cycle is currently halted. Returns nil if
// neither status nor plan is available, or nothing is halted.
func buildStopReasons(status *sakutei.StatusResponse, plan *sakutei.PlanResponse) []StopReason {
	var out []StopReason
	if status != nil {
		for _, r := range status.Resources {
			if r.ApplyStopped == nil {
				continue
			}
			reason := r.ApplyStopped.Reason
			if reason == "" {
				reason = r.ApplyStopped.Note
			}
			out = append(out, StopReason{Scope: "resource", Target: r.LocalID, Reason: reason})
		}
	}
	if plan != nil {
		if plan.Budget != nil && plan.Budget.Halted {
			out = append(out, StopReason{Scope: "budget", Reason: plan.Budget.Reason})
		}
		if plan.Capacity != nil && plan.Capacity.Halted {
			out = append(out, StopReason{Scope: "capacity", Reason: plan.Capacity.Reason})
		}
		for _, ph := range plan.PruneHalts {
			out = append(out, StopReason{Scope: "prune", Target: ph.Provider, Reason: ph.Reason})
		}
	}
	return out
}

// buildGraph converts Sakutei's /graph response into a SakuteiGraph grouped
// by wave, using each GraphResponse.Nodes[].Wave for wave membership and
// GraphResponse.Nodes for each resource's ready/waiting_for detail.
//
// This deliberately does not join against GraphResponse.Waves (the
// [][]string list of per-wave members): per Sakutei's docs/read-api.md,
// those strings are each node's local_id, not its full id (local_id is
// only unique within a provider+scope, not globally), while the rest of
// this view keys resources by the full id. Grouping by Nodes[].Wave
// directly avoids relying on a cross-provider-unique local_id and matches
// Wave's own documented meaning ("this node's index into Waves").
// External nodes (Wave -1, observed only as an upstream owner, never
// applied locally) are omitted rather than collected into a synthetic
// extra wave.
func buildGraph(g sakutei.GraphResponse) *SakuteiGraph {
	numWaves := len(g.Waves)
	waves := make([][]GraphNode, numWaves)
	for i := range waves {
		waves[i] = []GraphNode{}
	}
	for _, n := range g.Nodes {
		if n.External || n.Wave < 0 || n.Wave >= numWaves {
			continue
		}
		waves[n.Wave] = append(waves[n.Wave], GraphNode{
			ID:         n.ID,
			Wave:       n.Wave,
			Ready:      n.Ready,
			WaitingFor: n.WaitingFor,
			External:   n.External,
		})
	}
	for i := range waves {
		sort.Slice(waves[i], func(a, b int) bool { return waves[i][a].ID < waves[i][b].ID })
	}
	return &SakuteiGraph{Waves: waves}
}

// sakuteiNodeNameFromLocalID extracts the node-name candidate from a
// Sakutei resource's local_id, per the generic convention described in the
// package doc: everything up to (not including) the first "/" when the
// local_id contains one (e.g. "fw1/inet/filter/forward" -> "fw1"), or the
// whole local_id when it doesn't. It does not check whether the result is
// an actual node name -- callers decide that by looking it up in the
// Snapshot's own node list (byName), same as any explicit binding.
func sakuteiNodeNameFromLocalID(localID string) string {
	if node, _, found := strings.Cut(localID, "/"); found {
		return node
	}
	return localID
}

// explicitSakuteiBindings builds a Sakutei-resource-id -> node-name lookup
// out of a device plugin's optional StatusResponse.SakuteiBindings
// (contract v0.3). Returns nil (not an empty map) when status is nil or has
// no bindings at all -- a v0.1/v0.2 plugin, or a v0.3 plugin that simply
// doesn't need any -- so callers can treat a nil result the same as "fall
// back to the generic rule for everything" without allocating.
func explicitSakuteiBindings(status *plugin.StatusResponse) map[string]string {
	if status == nil || len(status.SakuteiBindings) == 0 {
		return nil
	}
	out := make(map[string]string, len(status.SakuteiBindings))
	for _, b := range status.SakuteiBindings {
		out[b.ResourceID] = b.Node
	}
	return out
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
	sakuteiPlan := a.fetchSakuteiPlan(ctx)
	sakuteiGraph := a.fetchSakuteiGraph(ctx)
	pluginStatus := a.fetchPluginStatus(ctx)

	nodes := make([]Node, len(topo.Nodes))
	byName := make(map[string]int, len(topo.Nodes))
	for i, n := range topo.Nodes {
		nodes[i] = Node{Node: n}
		byName[n.Name] = i
	}

	links := topo.Links
	if links == nil {
		// Keep this an empty array rather than JSON null in the common case
		// (containerlab has no links to report yet, or was never reachable):
		// callers treat Nodes/Links as always-arrays, matching Nodes above.
		links = []containerlab.Link{}
	}

	snap := Snapshot{
		GeneratedAt: time.Now().UTC(),
		Nodes:       nodes,
		Links:       links,
		Health:      a.healthSnapshot(),
	}

	if stopReasons := buildStopReasons(sakuteiStatus, sakuteiPlan); len(stopReasons) > 0 {
		snap.SakuteiStopReasons = stopReasons
	}
	if sakuteiGraph != nil {
		snap.SakuteiGraph = buildGraph(*sakuteiGraph)
	}

	bindings := explicitSakuteiBindings(pluginStatus)

	if sakuteiStatus != nil {
		summary := sakuteiStatus.Summary
		snap.SakuteiSummary = &summary
		for _, r := range sakuteiStatus.Resources {
			nodeName, explicit := bindings[r.ID]
			if !explicit {
				nodeName = sakuteiNodeNameFromLocalID(r.LocalID)
			}
			idx, ok := byName[nodeName]
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
