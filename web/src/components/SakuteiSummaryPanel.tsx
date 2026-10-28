import type { SakuteiSummary } from '../api/types'

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

export function SakuteiSummaryPanel({
  summary,
}: {
  summary?: SakuteiSummary
}) {
  if (!summary) {
    return (
      <p style={{ color: 'var(--text-muted)' }}>
        No Sakutei summary yet (not configured, or still observing).
      </p>
    )
  }

  return (
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
  )
}
