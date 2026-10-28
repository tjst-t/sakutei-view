import type { CSSProperties } from 'react'
import type { SnapshotNode } from '../api/types'
import { statusColor, statusLabel } from './colors'
import { StatusDot } from './StatusDot'

/**
 * A plain list of nodes and their color-coded state. This stands in for
 * the real topology diagram (Cytoscape.js, see Backlog item-2) until that
 * lands; it already reads the same Snapshot the diagram will.
 */
export function NodeList({ nodes }: { nodes: SnapshotNode[] }) {
  if (nodes.length === 0) {
    return (
      <p style={{ color: 'var(--text-muted)' }}>
        No nodes in the current topology.
      </p>
    )
  }

  return (
    <table style={{ width: '100%', borderCollapse: 'collapse' }}>
      <thead>
        <tr style={{ textAlign: 'left', color: 'var(--text-muted)' }}>
          <th style={th}>Node</th>
          <th style={th}>Kind</th>
          <th style={th}>containerlab</th>
          <th style={th}>Status</th>
        </tr>
      </thead>
      <tbody>
        {nodes.map((n) => (
          <tr key={n.name} style={{ borderTop: '1px solid var(--border)' }}>
            <td className="mono" style={td}>
              {n.name}
            </td>
            <td style={{ ...td, color: 'var(--text-muted)' }}>{n.kind ?? '—'}</td>
            <td style={{ ...td, color: 'var(--text-muted)' }}>
              {n.state ?? '—'}
              {n.status ? ` (${n.status})` : ''}
            </td>
            <td style={td}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <StatusDot color={statusColor(n.color)} />
                {statusLabel(n.color)}
                {n.sakutei?.drifted_fields?.length ? (
                  <span
                    className="mono"
                    style={{ color: 'var(--text-faint)', fontSize: 12 }}
                  >
                    ({n.sakutei.drifted_fields.join(', ')})
                  </span>
                ) : null}
              </div>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

const th: CSSProperties = { padding: '6px 10px 6px 0', fontWeight: 500 }
const td: CSSProperties = { padding: '8px 10px 8px 0' }
