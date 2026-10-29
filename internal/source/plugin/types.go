// Package plugin is an HTTP client for the device plugin contract
// (docs/plugin-contract.md, v0.2 — see that document's "Changes in v0.2"
// section for what changed from v0.1, and note that this client reads both
// versions without special-casing either: the v0.2 fields it added
// (ActionResult.BusyBy, the "rejected"/"cancelled" Run.Status values) are
// simply absent/unused in a v0.1 plugin's responses). It only knows how to
// call the contract's endpoints and decode their responses; it has no
// opinion about how a particular plugin is deployed.
package plugin

import (
	"encoding/json"
	"time"
)

// Info is the response of GET /plugin.
type Info struct {
	Version      string   `json:"version"`
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
}

// HasCapability reports whether cap is listed in Capabilities.
func (i Info) HasCapability(cap string) bool {
	for _, c := range i.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// Topology is the response of GET /plugin/topology.
type Topology struct {
	Inspect  json.RawMessage `json:"inspect"`
	ClabYAML string          `json:"clab_yaml"`
}

// Resource is one element of StatusResponse.Resources.
type Resource struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	State string `json:"state"` // ok | drifted | unknown | error
	// Node is the topology node name (a .clab.yml node key, the same
	// identifier containerlab.Node.Name uses -- never the underlying Docker
	// container name, see containerlab.Node's doc comment) that this
	// resource belongs to, if any. Clarified in contract v0.3 to spell out
	// "topology node name" explicitly; this is not a behavior change, just a
	// wording fix (plugins were always expected to send the topology name
	// here).
	Node          string         `json:"node"`
	Fields        map[string]any `json:"fields"`
	DriftedFields []string       `json:"drifted_fields"`
}

// SakuteiBinding explicitly maps a Sakutei resource id to the topology node
// it belongs to, for resources whose Sakutei local_id does not start with
// the node's name followed by "/" (the generic rule aggregate.Aggregator
// otherwise applies) -- e.g. a local_id with a provider-specific shape
// that merely happens to not be node-prefixed, or one that needs to bind
// to a different node than a literal prefix match would find. Added in
// contract v0.3; entirely optional, and takes priority over the generic
// prefix rule when both would apply to the same resource.
type SakuteiBinding struct {
	ResourceID string `json:"resource_id"`
	Node       string `json:"node"`
}

// SakuteiSource is one element of StatusResponse.SakuteiSources (added in
// contract v0.4): where a Sakutei resource's desired state is declared, so
// sakutei-view can show not just what Sakutei observed but where that came
// from and why.
type SakuteiSource struct {
	// ResourceID is the Sakutei resource id this entry is about, in the
	// same id shape SakuteiBinding.ResourceID uses (Sakutei's globally
	// unique "id", not its per-resource "local_id").
	ResourceID string `json:"resource_id"`
	// File is the declaration file this resource's desired state is
	// written in, or nil if it has none of its own -- see Derived and
	// DerivedFrom for that case. A nil File should be shown separately,
	// labeled as not itself a declaration, not hidden.
	File *string `json:"file"`
	// Key locates the specific item within File that declares this
	// resource, e.g. "resources[name=db1]" to address one entry of an
	// array by a stable field rather than by numeric index (which shifts as
	// the file is edited). Free-form; sakutei-view does not parse it, only
	// displays it.
	Key string `json:"key"`
	// Why is the human-readable rationale carried alongside the
	// declaration, or nil if the declaration has none. A nil Why should be
	// shown as "no rationale recorded" (sakutei-view: 理由の記載なし), not
	// hidden -- the absence itself is worth surfacing, not papering over.
	Why *string `json:"why"`
	// Derived is true when this resource's state is not written directly in
	// a file of its own (File is then nil) but computed/generated from one
	// or more other declarations -- see DerivedFrom. Optional; false/absent
	// means "not derived" (the ordinary case).
	Derived bool `json:"derived,omitempty"`
	// DerivedFrom lists what this resource was derived from (file names or
	// other plugin-chosen identifiers), used when Derived is true and File
	// is nil. Optional; empty/absent when Derived is false.
	DerivedFrom []string `json:"derived_from,omitempty"`
}

// PolicySource is one element of StatusResponse.PolicySources (added in
// contract v0.4): where one of the boundary firewall's permit rules is
// declared, and why.
type PolicySource struct {
	Name  string `json:"name"`
	From  string `json:"from"`
	To    string `json:"to"`
	Proto string `json:"proto"`
	Ports []int  `json:"ports"`
	// File, Key and Why follow the same meaning as SakuteiSource's fields of
	// the same name, above.
	File *string `json:"file"`
	Key  string  `json:"key"`
	Why  *string `json:"why"`
}

// StatusResponse is the response of GET /plugin/status.
type StatusResponse struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Resources   []Resource `json:"resources"`
	// SakuteiBindings optionally maps specific Sakutei resource ids to
	// topology node names, for cases the generic local_id-prefix rule
	// (aggregate.Aggregator) gets wrong or can't apply to. Added in contract
	// v0.3; omitted entirely by v0.1/v0.2 plugins, which is indistinguishable
	// from an empty list -- callers fall back to the generic rule for every
	// resource in that case, same as before this field existed.
	SakuteiBindings []SakuteiBinding `json:"sakutei_bindings,omitempty"`
	// SakuteiSources optionally says, for some or all Sakutei resources this
	// deployment cares about, which declaration file (and which item within
	// it) their desired state comes from, and why. Added in contract v0.4;
	// omitted entirely by a v0.1/v0.2/v0.3 plugin, which is indistinguishable
	// from an empty list -- sakutei-view simply has no source-file
	// information to show for any resource in that case.
	SakuteiSources []SakuteiSource `json:"sakutei_sources,omitempty"`
	// PolicySources optionally says, for some or all of the boundary
	// firewall's permit rules, which declaration file (and which item
	// within it) they come from, and why. Added in contract v0.4; same
	// optionality as SakuteiSources.
	PolicySources []PolicySource `json:"policy_sources,omitempty"`
}

// Script is one element of ScriptsResponse.Scripts.
type Script struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Actions     []string `json:"actions"`
}

// ScriptsResponse is the response of GET /plugin/scripts.
type ScriptsResponse struct {
	Scripts []Script `json:"scripts"`
}

// ActionResult is the response of POST /plugin/scripts/{id}/actions/{action},
// for both the 202 Accepted and 409 Conflict cases.
type ActionResult struct {
	Accepted bool `json:"accepted"`
	// RunID is the run id to poll via GET /plugin/runs/{run_id}. Always set
	// when Accepted is true. When Accepted is false (409 Conflict), it is
	// set only if the conflict is one of this plugin's own runs (BusyBy ==
	// "run"); as of contract v0.2 it may be empty when some other, external
	// process this plugin does not track as a run holds the exclusivity
	// instead (BusyBy == "external") — for example, the deployment's whole
	// environment is being rebuilt by something outside this plugin. A v0.1
	// plugin always sets RunID on a 409, since v0.1 only has the "run" case.
	RunID string `json:"run_id,omitempty"`
	// Reason is a free-form, human-readable explanation. Set whenever
	// Accepted is false.
	Reason string `json:"reason,omitempty"`
	// BusyBy says what is holding exclusivity when Accepted is false: "run"
	// if it is one of this plugin's own runs (RunID is then set), or
	// "external" if something outside this plugin's own run-tracking holds
	// it (RunID is then empty). Added in contract v0.2; empty/absent from a
	// v0.1 plugin's response, which a caller should treat the same as
	// "run" (v0.1's only case, and RunID being set is itself evidence of
	// it).
	BusyBy string `json:"busy_by,omitempty"`
}

// Run is the response of GET /plugin/runs/{run_id}.
type Run struct {
	RunID    string `json:"run_id"`
	ScriptID string `json:"script_id"`
	Action   string `json:"action"`
	// Status is one of: running, succeeded, failed (v0.1), plus, as of
	// v0.2, rejected (the run could not be started at all) and cancelled
	// (the run was stopped before finishing). A caller that encounters a
	// status it does not recognize — from a plugin implementing some future
	// contract version — should render it as an unknown, terminal state
	// rather than erroring or polling forever.
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	Message    string     `json:"message"`
}

// KnownRunStatuses are the Run.Status values defined by contract v0.1/v0.2.
// A caller should treat any other string the same as it would treat none of
// these — an unrecognized, terminal status — rather than failing to decode
// (Run.Status is a plain string precisely so a new contract version can add
// statuses without breaking older clients).
var KnownRunStatuses = []string{"running", "succeeded", "failed", "rejected", "cancelled"}

// RunIsInProgress reports whether status represents a run that is still
// going (only "running"). Any other value, including one this contract
// version does not define, is treated as finished/terminal so a caller
// polling GET /plugin/runs/{run_id} does not loop forever on an unexpected
// status.
func RunIsInProgress(status string) bool {
	return status == "running"
}

// Event is one event from GET /plugin/events.
type Event struct {
	Kind       string    `json:"kind"`
	Level      string    `json:"level"` // firing | resolved
	ResourceID string    `json:"resource_id,omitempty"`
	Message    string    `json:"message"`
	At         time.Time `json:"at"`
}
