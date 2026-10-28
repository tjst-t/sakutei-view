import type { NodeColor } from '../api/types'

/**
 * Maps the backend's Color vocabulary to this palette's status colors and
 * a short human label. Kept in one place so every view (the node list, the
 * topology diagram, the DAG later) reads a node's state the same way.
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

/**
 * Same mapping as statusColor, but as a literal hex value rather than a CSS
 * custom-property reference. Cytoscape.js renders to a canvas element and
 * evaluates its own style sheet independently of the page's CSS engine, so
 * `var(--status-ok)` is not a color it can resolve — it needs the literal
 * value. Kept in sync with the --status-* tokens in index.css by hand;
 * there are only five of them.
 */
export function statusColorHex(color: NodeColor): string {
  switch (color) {
    case 'ok':
      return '#39d98a'
    case 'drift':
      return '#e3b341'
    case 'down':
    case 'apply_failed':
      return '#e5534b'
    case 'workflow':
      return '#4fa3e3'
    case 'unobservable':
    case 'unmanaged':
    case 'unknown':
    default:
      return '#6c7881'
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
