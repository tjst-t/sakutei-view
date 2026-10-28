import type { SourceHealth, SourceName } from '../api/types'
import { StatusDot } from './StatusDot'

const LABELS: Record<SourceName, string> = {
  containerlab: 'containerlab',
  sakutei: 'Sakutei',
  plugin: 'Device plugin',
}

const ORDER: SourceName[] = ['containerlab', 'sakutei', 'plugin']

function describe(h: SourceHealth): { color: string; text: string } {
  if (!h.configured) {
    return { color: 'var(--text-faint)', text: 'not configured' }
  }
  if (h.ok) {
    return { color: 'var(--status-ok)', text: 'reachable' }
  }
  return { color: 'var(--status-down)', text: 'unreachable' }
}

function formatTime(iso?: string): string | null {
  if (!iso) return null
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return null
  return d.toLocaleString()
}

export function SourceHealthPanel({
  health,
}: {
  health: Record<SourceName, SourceHealth>
}) {
  return (
    <section>
      <h2 style={{ fontSize: 15, fontWeight: 600, letterSpacing: 0.1, margin: '0 0 14px' }}>
        Sources
      </h2>
      <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {ORDER.map((name) => {
          const h = health[name]
          if (!h) return null
          const { color, text } = describe(h)
          const lastSuccess = formatTime(h.last_success)
          return (
            <li
              key={name}
              style={{
                padding: '10px 0',
                borderTop: '1px solid var(--border)',
              }}
            >
              <div
                style={{ display: 'flex', alignItems: 'center', gap: 8 }}
              >
                <StatusDot color={color} />
                <span style={{ fontWeight: 500 }}>{LABELS[name]}</span>
                <span style={{ color: 'var(--text-muted)', marginLeft: 'auto' }}>
                  {text}
                </span>
              </div>
              {h.configured && lastSuccess && (
                <div
                  className="mono"
                  style={{ fontSize: 12, color: 'var(--text-faint)', marginTop: 4 }}
                >
                  last read ok: {lastSuccess}
                </div>
              )}
              {h.configured && !h.ok && h.last_error && (
                <div
                  className="mono"
                  style={{
                    fontSize: 12,
                    color: 'var(--status-down)',
                    marginTop: 4,
                    wordBreak: 'break-word',
                  }}
                >
                  {h.last_error}
                </div>
              )}
            </li>
          )
        })}
      </ul>
    </section>
  )
}
