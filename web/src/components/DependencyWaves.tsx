import type { SakuteiGraph } from '../api/types'
import { StatusDot } from './StatusDot'

/**
 * Sakutei's dependency graph (GET /graph), shown as its waves: the order
 * resources become ready to apply in. Each column is one wave; a resource
 * that isn't ready yet shows what it's waiting for. This is deliberately a
 * column layout rather than a force-directed graph (Cytoscape is already
 * used for the topology diagram above) — waves are a strict left-to-right
 * order, which a list reads better than a graph layout would.
 */
export function DependencyWaves({ graph }: { graph?: SakuteiGraph }) {
  if (!graph || graph.waves.length === 0) {
    return (
      <p style={{ color: 'var(--text-muted)' }}>
        No Sakutei dependency graph yet (not configured, or still observing).
      </p>
    )
  }

  return (
    <div style={{ display: 'flex', gap: 14, overflowX: 'auto', paddingBottom: 4 }}>
      {graph.waves.map((wave, i) => (
        <div
          key={i}
          style={{
            minWidth: 170,
            flexShrink: 0,
            border: '1px solid var(--border)',
            borderRadius: 6,
            padding: 10,
            display: 'flex',
            flexDirection: 'column',
            gap: 8,
            background: 'var(--surface-raised)',
          }}
        >
          <div
            className="mono"
            style={{
              fontSize: 11,
              color: 'var(--text-faint)',
              borderBottom: '1px solid var(--border)',
              paddingBottom: 6,
            }}
          >
            wave {i}
          </div>
          {wave.length === 0 ? (
            <span style={{ color: 'var(--text-faint)', fontSize: 12 }}>—</span>
          ) : (
            wave.map((n) => (
              <div
                key={n.id}
                style={{ display: 'flex', alignItems: 'flex-start', gap: 6 }}
              >
                <span style={{ marginTop: 3 }}>
                  <StatusDot
                    color={n.ready ? 'var(--status-ok)' : 'var(--status-neutral)'}
                    title={n.ready ? 'ready' : 'not ready'}
                  />
                </span>
                <div>
                  <div className="mono" style={{ fontSize: 12 }}>
                    {n.id}
                  </div>
                  {!n.ready && n.waiting_for && (
                    <div style={{ fontSize: 11, color: 'var(--text-faint)' }}>
                      waiting for {n.waiting_for}
                    </div>
                  )}
                </div>
              </div>
            ))
          )}
        </div>
      ))}
    </div>
  )
}
