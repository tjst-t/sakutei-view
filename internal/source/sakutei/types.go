// Package sakutei is an HTTP client for Sakutei's read-only daemon API
// (`--read-listen`): GET /status, /plan, /graph, and the /events
// Server-Sent-Events stream. The JSON shapes here follow Sakutei v2's
// docs/read-api.md, docs/status-json.md and docs/plan-json.md (schema_version
// 1). Fields sakutei-view does not use are intentionally omitted — JSON
// decoding ignores fields it doesn't recognize, and the Sakutei docs
// explicitly say readers should do the same.
package sakutei

// CycleInfo is embedded in the /status, /plan and /graph responses: which
// reconcile cycle produced the data.
type CycleInfo struct {
	// Cycle is the cycle number that produced this snapshot (the one that
	// last finished Observe).
	Cycle int `json:"cycle"`
	// CycleTime is when that cycle ended (UTC, RFC 3339).
	CycleTime string `json:"cycle_time"`
	// LastCycle is the most recent cycle the daemon has run at all. If it is
	// greater than Cycle, later cycles did not reach Observe (skipped input,
	// unreadable intent, etc.) and the data below is from the last cycle
	// that did.
	LastCycle int `json:"last_cycle"`
}

// FieldDiff is one drifted or changing field: From is the observed
// (real-world) value, To is the declared value. Secret values appear as
// "secretref:..." references, never in the clear.
type FieldDiff struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// Mirror is the git-distribution intent-fetch status, part of DaemonHealth.
type Mirror struct {
	Remote    string `json:"remote"`
	Revision  string `json:"revision"`
	FetchedAt string `json:"fetched_at,omitempty"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// DaemonHealth is the daemon's own health/distribution status, present on
// /status when the daemon can read its own status file.
type DaemonHealth struct {
	Cycle                       int    `json:"cycle"`
	ConsecutiveUnreachableSkips int    `json:"consecutive_unreachable_skips"`
	UnreachableSkipLimit        int    `json:"unreachable_skip_limit"`
	ConsecutiveErrorCycles      int    `json:"consecutive_error_cycles"`
	LastError                   string `json:"last_error,omitempty"`
	Interval                    string `json:"interval,omitempty"`
	UpdatedAt                   string `json:"updated_at,omitempty"`
	Prolonged                   bool   `json:"prolonged"`
	// Stale is always false on the daemon's own GET /status.
	Stale bool `json:"stale"`

	Claim  string  `json:"claim,omitempty"`
	Idle   bool    `json:"idle,omitempty"`
	Shard  string  `json:"shard,omitempty"`
	Mirror *Mirror `json:"mirror,omitempty"`
}

// ----- GET /status -----------------------------------------------------

// Summary is the resource counts on /status, matching the "summary:" line
// of the text output.
type Summary struct {
	Conformant   int `json:"conformant"`
	Drift        int `json:"drift"`
	Missing      int `json:"missing"`
	Unobservable int `json:"unobservable"`
	Unmanaged    int `json:"unmanaged"`
	Workflow     int `json:"workflow"`
	Oscillating  int `json:"oscillating"`
	ListErrors   int `json:"list_errors"`
	Total        int `json:"total"`
}

// ApplyStopped describes why the daemon stopped applying a resource that
// kept oscillating (same Diff, no effect, past the retry limit).
type ApplyStopped struct {
	Diff               string `json:"diff,omitempty"`
	IneffectiveApplies int    `json:"ineffective_applies,omitempty"`
	MaxApplies         int    `json:"max_applies,omitempty"`
	Settle             string `json:"settle,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Note               string `json:"note,omitempty"`
	Since              string `json:"since,omitempty"`
}

// ResourceStatus is one element of StatusResponse.Resources.
type ResourceStatus struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Scope    map[string]string `json:"scope"`
	LocalID  string            `json:"local_id"`
	// State is one of: conformant, drift, unobservable, unmanaged,
	// workflow, oscillating.
	State string `json:"state"`

	Missing       bool          `json:"missing,omitempty"`
	Cause         string        `json:"cause,omitempty"`
	Fields        []FieldDiff   `json:"fields,omitempty"`
	Diagnostic    string        `json:"diagnostic,omitempty"`
	LastSeen      string        `json:"last_seen,omitempty"`
	Tier          string        `json:"tier,omitempty"`
	ObserveError  string        `json:"observe_error,omitempty"`
	Workflow      string        `json:"workflow,omitempty"`
	WorkflowStage string        `json:"workflow_stage,omitempty"`
	ApplyStopped  *ApplyStopped `json:"apply_stopped,omitempty"`
}

// UnmanagedUndetectable is one element of StatusResponse.UnmanagedUndetectable:
// a provider whose set of unmanaged (undeclared) resources could not be
// determined.
type UnmanagedUndetectable struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Reason   string `json:"reason,omitempty"`
	Note     string `json:"note,omitempty"`
}

// StatusResponse is the response of GET /status.
type StatusResponse struct {
	SchemaVersion         int                     `json:"schema_version"`
	GeneratedAt           string                  `json:"generated_at"`
	IntentRevision        string                  `json:"intent_revision,omitempty"`
	RenderedRevision      string                  `json:"rendered_revision,omitempty"`
	Complete              bool                    `json:"complete"`
	Summary               Summary                 `json:"summary"`
	Resources             []ResourceStatus        `json:"resources"`
	UnmanagedUndetectable []UnmanagedUndetectable `json:"unmanaged_undetectable,omitempty"`
	Daemon                *DaemonHealth           `json:"daemon,omitempty"`
	DaemonNote            string                  `json:"daemon_note,omitempty"`
	CycleInfo
}

// ----- GET /plan ---------------------------------------------------------

// PlanClaim is PlanResponse.Claim, present only when --claim was used.
type PlanClaim struct {
	Selector string `json:"selector"`
	InClaim  int    `json:"in_claim"`
	Excluded int    `json:"excluded"`
}

// Remediation is PlanResponse.Remediation.
type Remediation struct {
	Mode          string `json:"mode"`
	Reason        string `json:"reason,omitempty"`
	OutsideWindow bool   `json:"outside_window,omitempty"`
	Line          string `json:"line,omitempty"`
}

// PlanSummary is PlanResponse.Summary.
type PlanSummary struct {
	Changes            int `json:"changes"`
	Create             int `json:"create"`
	Update             int `json:"update"`
	Delete             int `json:"delete"`
	Destructive        int `json:"destructive"`
	Blocked            int `json:"blocked"`
	PlannedChangeLane  int `json:"planned_change_lane"`
	Task               int `json:"task"`
	Workflow           int `json:"workflow"`
	PendingAllocations int `json:"pending_allocations"`
	Unobservable       int `json:"unobservable"`
	PruneHalts         int `json:"prune_halts"`
}

// TaskInfo is Operation.Task: the operation is handed to the daemon's Task
// mechanism instead of being Applied directly.
type TaskInfo struct {
	Window         string `json:"window,omitempty"`
	RollbackOnFail bool   `json:"rollback_on_fail,omitempty"`
	Manual         bool   `json:"manual,omitempty"`
	Procedure      string `json:"procedure,omitempty"`
	ProcedureURL   string `json:"procedure_url,omitempty"`
}

// Operation is one element of PlanResponse.Operations.
type Operation struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Scope    map[string]string `json:"scope"`
	LocalID  string            `json:"local_id"`
	// Kind is CREATE, UPDATE or DELETE.
	Kind          string      `json:"kind"`
	Destructive   bool        `json:"destructive"`
	Prune         bool        `json:"prune"`
	Absent        bool        `json:"absent,omitempty"`
	Cause         string      `json:"cause,omitempty"`
	Diagnostic    string      `json:"diagnostic,omitempty"`
	Fields        []FieldDiff `json:"fields,omitempty"`
	Lane          string      `json:"lane"`
	PlannedChange string      `json:"planned_change,omitempty"`
	Blocked       bool        `json:"blocked"`
	BlockedBy     string      `json:"blocked_by,omitempty"`
	BlockReason   string      `json:"block_reason,omitempty"`
	Task          *TaskInfo   `json:"task,omitempty"`
}

// Gate is PlanResponse.Budget / PlanResponse.Capacity: whether a reconcile
// right now would be halted, and why.
type Gate struct {
	Halted bool   `json:"halted"`
	Halt   string `json:"halt,omitempty"` // "all" | "destructive"
	Reason string `json:"reason,omitempty"`
}

// PruneHalt is one element of PlanResponse.PruneHalts.
type PruneHalt struct {
	Provider    string `json:"provider"`
	Reason      string `json:"reason,omitempty"`
	CountDrop   bool   `json:"count_drop,omitempty"`
	HeldOrphans int    `json:"held_orphans,omitempty"`
}

// ResourceRef identifies a resource without full status/plan detail, used
// in several of PlanResponse's secondary lists.
type ResourceRef struct {
	ID       string            `json:"id"`
	Provider string            `json:"provider"`
	Scope    map[string]string `json:"scope"`
	LocalID  string            `json:"local_id"`
}

// WorkflowTarget is one element of PlanResponse.Workflow.
type WorkflowTarget struct {
	ResourceRef
	Workflow string `json:"workflow"`
	Stage    string `json:"stage"`
}

// PendingAllocation is one element of PlanResponse.PendingAllocations.
type PendingAllocation struct {
	ResourceRef
	Field         string `json:"field"`
	Pool          string `json:"pool"`
	FreeKnown     bool   `json:"free_known"`
	Free          int    `json:"free,omitempty"`
	PendingInPool int    `json:"pending_in_pool,omitempty"`
	Error         string `json:"error,omitempty"`
	Line          string `json:"line,omitempty"`
}

// UnobservableRef is one element of PlanResponse.Unobservable.
type UnobservableRef struct {
	ResourceRef
	Availability string `json:"availability"` // "unknown" | "converging"
}

// Composite is one element of PlanResponse.Composites.
type Composite struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// PlanResponse is the response of GET /plan.
type PlanResponse struct {
	SchemaVersion       int                 `json:"schema_version"`
	GeneratedAt         string              `json:"generated_at"`
	IntentRevision      string              `json:"intent_revision,omitempty"`
	RenderedRevision    string              `json:"rendered_revision,omitempty"`
	Error               string              `json:"error,omitempty"`
	Claim               *PlanClaim          `json:"claim,omitempty"`
	Remediation         *Remediation        `json:"remediation,omitempty"`
	Summary             PlanSummary         `json:"summary"`
	Operations          []Operation         `json:"operations"`
	Budget              *Gate               `json:"budget,omitempty"`
	Capacity            *Gate               `json:"capacity,omitempty"`
	PlannedChangeEvents []string            `json:"planned_change_events,omitempty"`
	PruneHalts          []PruneHalt         `json:"prune_halts,omitempty"`
	PruneDeferred       []ResourceRef       `json:"prune_deferred,omitempty"`
	Workflow            []WorkflowTarget    `json:"workflow,omitempty"`
	PendingAllocations  []PendingAllocation `json:"pending_allocations,omitempty"`
	Unobservable        []UnobservableRef   `json:"unobservable,omitempty"`
	Composites          []Composite         `json:"composites,omitempty"`
	CycleInfo
}

// ----- GET /graph --------------------------------------------------------

// GraphNode is one element of GraphResponse.Nodes.
type GraphNode struct {
	ID         string            `json:"id"`
	Provider   string            `json:"provider"`
	Scope      map[string]string `json:"scope"`
	LocalID    string            `json:"local_id"`
	DependsOn  []string          `json:"depends_on"`
	External   bool              `json:"external,omitempty"`
	Observed   bool              `json:"observed"`
	Conditions map[string]bool   `json:"conditions,omitempty"`
	Ready      bool              `json:"ready"`
	WaitingFor string            `json:"waiting_for,omitempty"`
	// Wave is this node's index into GraphResponse.Waves, or -1 for
	// External nodes.
	Wave int `json:"wave"`
}

// GraphEdge is one element of GraphResponse.Edges.
type GraphEdge struct {
	From           string `json:"from"`
	To             string `json:"to"`
	ReadyCondition string `json:"ready_condition,omitempty"`
	// Ready is only meaningful (and only present) when ReadyCondition is set.
	Ready *bool `json:"ready,omitempty"`
}

// GraphSummary is GraphResponse.Summary.
type GraphSummary struct {
	Nodes    int `json:"nodes"`
	External int `json:"external"`
	Edges    int `json:"edges"`
	Waves    int `json:"waves"`
	NotReady int `json:"not_ready"`
}

// GraphResponse is the response of GET /graph.
type GraphResponse struct {
	SchemaVersion    int          `json:"schema_version"`
	GeneratedAt      string       `json:"generated_at"`
	IntentRevision   string       `json:"intent_revision,omitempty"`
	RenderedRevision string       `json:"rendered_revision,omitempty"`
	Claim            string       `json:"claim,omitempty"`
	Nodes            []GraphNode  `json:"nodes"`
	Edges            []GraphEdge  `json:"edges"`
	Waves            [][]string   `json:"waves"`
	Summary          GraphSummary `json:"summary"`
	CycleInfo
}

// ----- /events (Server-Sent Events) --------------------------------------

// Event mirrors pkg/core.Event's JSON shape (see Sakutei's
// docs/architecture and §22.3). Kind/Severity/State/Class are free-form
// strings on the wire; Sakutei defines a fixed set of values but
// sakutei-view treats unrecognized ones generically.
type Event struct {
	Time             string            `json:"time"`
	Kind             string            `json:"kind"`
	Severity         string            `json:"severity"`
	Cycle            int               `json:"cycle,omitempty"`
	Resource         string            `json:"resource,omitempty"`
	Scope            string            `json:"scope,omitempty"`
	IntentRevision   string            `json:"intent_revision,omitempty"`
	RenderedRevision string            `json:"rendered_revision,omitempty"`
	Cause            string            `json:"cause,omitempty"`
	Message          string            `json:"message"`
	Attrs            map[string]string `json:"attrs,omitempty"`
	// State is "firing" or "resolved" for level events (drift, halts,
	// reconcile failures); empty for one-shot edge events.
	State string `json:"state,omitempty"`
	Since string `json:"since,omitempty"`
	Count int    `json:"count,omitempty"`
	Class string `json:"class,omitempty"`
}
