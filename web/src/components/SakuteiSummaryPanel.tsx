import type { SakuteiSummary, StopReason } from '../api/types'
import { StatusDot } from './StatusDot'

const ROWS: Array<{ key: keyof SakuteiSummary; label: string; color?: string }> = [
  { key: 'conformant', label: 'Conformant', color: 'var(--status-ok)' },
  { key: 'drift', label: 'Drift', color: 'var(--status-drift)' },
  { key: 'missing', label: 'Missing', color: 'var(--status-down)' },
  { key: 'oscillating', label: 'Oscillating', color: 'var(--status-down)' },
  { key: 'workflow', label: 'Workflow', color: 'var(--status-progress)' },
  { key: 'unobservable', label: 'Unobservable' },
  { key: 'unmanaged', label: 'Unmanaged' },
  { key: 'list_errors', label: 'List errors', color: 'var(--status-down)' },
]

const SCOPE_LABEL: Record<StopReason['scope'], string> = {
  resource: 'resource',
  budget: 'budget gate',
  capacity: 'capacity gate',
  prune: 'prune',
}

export function SakuteiSummaryPanel({
  summary,
  stopReasons,
}: {
  summary?: SakuteiSummary
  stopReasons?: StopReason[]
}) {
  if (!summary) {
    return (
      <p style={{ color: 'var(--text-muted)' }}>
        No Sakutei summary yet (not configured, or still observing).
      </p>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit, minmax(110px, 1fr))',
          gap: 10,
        }}
      >
        {ROWS.filter((r) => summary[r.key] > 0 || r.key === 'conformant').map(
          (r) => (
            <div key={r.key}>
              <div
                className="mono"
                style={{
                  fontSize: 22,
                  fontWeight: 600,
                  color: r.color ?? 'var(--text)',
                }}
              >
                {summary[r.key]}
              </div>
              <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
                {r.label}
              </div>
            </div>
          ),
        )}
      </div>

      {stopReasons && stopReasons.length > 0 && (
        <div>
          <h3
            style={{
              fontSize: 11,
              fontWeight: 600,
              textTransform: 'uppercase',
              letterSpacing: 0.4,
              margin: '0 0 8px',
              color: 'var(--text-faint)',
            }}
          >
            Halted ({stopReasons.length})
          </h3>
          <ul
            style={{
              listStyle: 'none',
              margin: 0,
              padding: 0,
              display: 'flex',
              flexDirection: 'column',
              gap: 6,
            }}
          >
            {stopReasons.map((r, i) => (
              <li
                key={i}
                style={{ display: 'flex', alignItems: 'flex-start', gap: 8, fontSize: 12 }}
              >
                <span style={{ marginTop: 3 }}>
                  <StatusDot color="var(--status-down)" />
                </span>
                <span>
                  <span className="mono" style={{ color: 'var(--text-muted)' }}>
                    {SCOPE_LABEL[r.scope]}
                    {r.target ? `: ${r.target}` : ''}
                  </span>
                  {r.reason ? <span style={{ color: 'var(--text)' }}> — {r.reason}</span> : null}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
