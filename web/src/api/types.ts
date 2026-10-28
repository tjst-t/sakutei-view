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
  health: Record<SourceName, SourceHealth>
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
