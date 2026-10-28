// Mirrors the JSON shapes served by sakutei-view's Go backend
// (internal/api/types.go, internal/aggregate/aggregate.go,
// internal/aggregate/events.go, internal/source/sakutei/types.go). Kept
// hand-written rather than generated: the backend's JSON surface is small
// and stable, and a generator would be more machinery than this needs.

/** aggregate.Color */
export type NodeColor =
  | 'down'
  | 'ok'
  | 'drift'
  | 'apply_failed'
  | 'unobservable'
  | 'unmanaged'
  | 'workflow'
  | 'unknown'

/** aggregate.SourceName, used as the keys of Snapshot.health */
export type SourceName = 'containerlab' | 'sakutei' | 'plugin'

/** aggregate.SourceHealth */
export interface SourceHealth {
  configured: boolean
  ok: boolean
  last_success?: string
  last_error?: string
}

/** aggregate.SakuteiOverlay */
export interface SakuteiOverlay {
  resource_id: string
  state: string
  apply_failed?: boolean
  drifted_fields?: string[]
}

/** aggregate.PluginOverlay */
export interface PluginOverlay {
  resource_id: string
  state: string
  drifted_fields?: string[]
}

/** containerlab.Node, plus aggregate's overlays and computed Color */
export interface SnapshotNode {
  lab_name?: string
  name: string
  kind?: string
  image?: string
  state?: string
  status?: string
  ipv4?: string
  ipv6?: string
  owner?: string
  group?: string
  sakutei?: SakuteiOverlay
  plugin?: PluginOverlay
  color: NodeColor
}

/** containerlab.LinkEndpoint */
export interface LinkEndpoint {
  node: string
  interface?: string
  mac?: string
  state?: string
}

/** containerlab.Link */
export interface SnapshotLink {
  type: string
  a: LinkEndpoint
  b: LinkEndpoint
}

/** aggregate.StopReason */
export interface StopReason {
  scope: 'resource' | 'budget' | 'capacity' | 'prune'
  target?: string
  reason?: string
}

/** aggregate.GraphNode */
export interface SakuteiGraphNode {
  id: string
  wave: number
  ready: boolean
  waiting_for?: string
  external?: boolean
}

/** aggregate.SakuteiGraph */
export interface SakuteiGraph {
  waves: SakuteiGraphNode[][]
}

/** sakutei.Summary */
export interface SakuteiSummary {
  conformant: number
  drift: number
  missing: number
  unobservable: number
  unmanaged: number
  workflow: number
  oscillating: number
  list_errors: number
  total: number
}

/** The exact JSON shape of GET /api/snapshot (internal/api/types.go). */
export interface Snapshot {
  generated_at: string
  nodes: SnapshotNode[]
  links: SnapshotLink[]
  sakutei_summary?: SakuteiSummary
  sakutei_stop_reasons?: StopReason[]
  sakutei_graph?: SakuteiGraph
  health: Record<SourceName, SourceHealth>
  /** Whether POST /api/actions/... and friends are available on this
   * deployment (a plugin and Basic auth are both configured). When false,
   * the frontend must not render an operations panel at all. */
  actions_enabled: boolean
}

/** aggregate.EventSource */
export type EventSourceName = 'sakutei' | 'plugin'

/** aggregate.Event, one line of GET /api/events */
export interface LiveEvent {
  source: EventSourceName
  time: string
  kind: string
  severity?: string
  state?: string
  resource?: string
  message: string
}

/** plugin.Script */
export interface PluginScript {
  id: string
  label: string
  description?: string
  actions: string[]
}

/** plugin.ScriptsResponse, the body of GET /api/scripts */
export interface PluginScriptsResponse {
  scripts: PluginScript[]
}

/**
 * plugin.ActionResult, the body of POST /api/actions/{scriptID}/{action}
 * for both the 202 Accepted and 409 Conflict cases.
 */
export interface PluginActionResult {
  accepted: boolean
  run_id?: string
  reason?: string
  busy_by?: string
}

/**
 * plugin.Run, the body of GET /api/runs/{runID}. status is intentionally a
 * plain string, not a closed union: the contract documents
 * running/succeeded/failed/rejected/cancelled, but a future contract
 * version may add more, and a value this view does not recognize should
 * render as an unknown, terminal state rather than erroring.
 */
export interface PluginRun {
  run_id: string
  script_id: string
  action: string
  status: string
  started_at: string
  finished_at?: string | null
  message?: string
}

/** Whether a PluginRun.status represents a run that is still going. */
export function runIsInProgress(status: string): boolean {
  return status === 'running'
}
