import type { NodeColor } from '../api/types'

/**
 * Maps the backend's Color vocabulary to this palette's status colors and
 * a short human label. Kept in one place so every view (the node list now,
 * the topology diagram and DAG later) reads a node's state the same way.
 */
export function statusColor(color: NodeColor): string {
  switch (color) {
    case 'ok':
      return 'var(--status-ok)'
    case 'drift':
      return 'var(--status-drift)'
    case 'down':
    case 'apply_failed':
      return 'var(--status-down)'
    case 'workflow':
      return 'var(--status-progress)'
    case 'unobservable':
    case 'unmanaged':
    case 'unknown':
    default:
      return 'var(--status-neutral)'
  }
}

export function statusLabel(color: NodeColor): string {
  switch (color) {
    case 'ok':
      return 'OK'
    case 'drift':
      return 'Drift'
    case 'down':
      return 'Down'
    case 'apply_failed':
      return 'Apply failed'
    case 'workflow':
      return 'Workflow'
    case 'unobservable':
      return 'Unobservable'
    case 'unmanaged':
      return 'Unmanaged'
    case 'unknown':
    default:
      return 'Unknown'
  }
}
