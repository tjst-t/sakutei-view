import type { EventStreamStatus } from '../api/client'
import type { LiveEvent } from '../api/types'
import { StatusDot } from './StatusDot'

const SOURCE_LABEL: Record<LiveEvent['source'], string> = {
  sakutei: 'Sakutei',
  plugin: 'plugin',
}

function formatTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleTimeString()
}

function stateColor(state?: string): string {
  if (state === 'firing') return 'var(--status-down)'
  if (state === 'resolved') return 'var(--status-ok)'
  return 'var(--text-faint)'
}

export function EventLog({
  events,
  streamStatus,
}: {
  events: LiveEvent[]
  streamStatus: EventStreamStatus
}) {
  return (
    <section>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          marginBottom: 10,
        }}
      >
        <h2 style={{ fontSize: 13, fontWeight: 600, margin: 0 }}>
          Live events
        </h2>
        <span
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 6,
            color: 'var(--text-muted)',
            fontSize: 12,
          }}
        >
          <StatusDot
            color={
              streamStatus === 'open'
                ? 'var(--status-ok)'
                : streamStatus === 'connecting'
                  ? 'var(--status-drift)'
                  : 'var(--status-down)'
            }
          />
          {streamStatus}
        </span>
      </div>
      {events.length === 0 ? (
        <p style={{ color: 'var(--text-muted)' }}>No events yet.</p>
      ) : (
        <ul
          className="mono"
          style={{
            listStyle: 'none',
            margin: 0,
            padding: 0,
            maxHeight: 260,
            overflowY: 'auto',
            fontSize: 12.5,
          }}
        >
          {events.map((ev, i) => (
            <li
              key={i}
              style={{
                display: 'flex',
                gap: 10,
                padding: '4px 0',
                borderTop: i > 0 ? '1px solid var(--border)' : undefined,
              }}
            >
              <span style={{ color: 'var(--text-faint)', flexShrink: 0 }}>
                {formatTime(ev.time)}
              </span>
              <span style={{ color: 'var(--text-muted)', flexShrink: 0 }}>
                {SOURCE_LABEL[ev.source]}
              </span>
              {ev.state && <StatusDot color={stateColor(ev.state)} />}
              <span style={{ color: 'var(--text-faint)', flexShrink: 0 }}>
                {ev.kind}
              </span>
              {ev.resource && (
                <span style={{ flexShrink: 0 }}>{ev.resource}</span>
              )}
              <span style={{ color: 'var(--text)' }}>{ev.message}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
