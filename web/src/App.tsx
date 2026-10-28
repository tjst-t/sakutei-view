import type { CSSProperties } from 'react'
import { useEffect, useRef, useState } from 'react'
import { fetchSnapshot, subscribeEvents, type EventStreamStatus } from './api/client'
import type { LiveEvent, Snapshot } from './api/types'
import { DependencyWaves } from './components/DependencyWaves'
import { EventLog } from './components/EventLog'
import { NodeList } from './components/NodeList'
import { SakuteiSummaryPanel } from './components/SakuteiSummaryPanel'
import { SourceHealthPanel } from './components/SourceHealthPanel'
import { StatusDot } from './components/StatusDot'
import { TopologyDiagram } from './components/TopologyDiagram'

// How often to re-fetch GET /api/snapshot in the background. Page (1)'s
// scope is a working read-only poll + SSE loop; a push-driven snapshot
// update (reacting to /api/events instead of a timer) is left for when the
// topology diagram (Backlog item-2) needs it to feel live rather than just
// correct.
const SNAPSHOT_POLL_INTERVAL_MS = 5000
const MAX_EVENTS_SHOWN = 200

export default function App() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null)
  const [snapshotError, setSnapshotError] = useState<string | null>(null)
  const [events, setEvents] = useState<LiveEvent[]>([])
  const [streamStatus, setStreamStatus] = useState<EventStreamStatus>('connecting')

  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()

    async function load() {
      try {
        const snap = await fetchSnapshot(controller.signal)
        if (!cancelled) {
          setSnapshot(snap)
          setSnapshotError(null)
        }
      } catch (err) {
        if (!cancelled && !controller.signal.aborted) {
          setSnapshotError(err instanceof Error ? err.message : String(err))
        }
      }
    }

    load()
    const interval = setInterval(load, SNAPSHOT_POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      controller.abort()
      clearInterval(interval)
    }
  }, [])

  const eventsRef = useRef<LiveEvent[]>([])
  useEffect(() => {
    const unsubscribe = subscribeEvents(
      (event) => {
        eventsRef.current = [event, ...eventsRef.current].slice(0, MAX_EVENTS_SHOWN)
        setEvents(eventsRef.current)
      },
      setStreamStatus,
    )
    return unsubscribe
  }, [])

  return (
    <div
      style={{
        minHeight: '100%',
        display: 'flex',
        flexDirection: 'column',
        borderTop: '2px solid var(--accent)',
      }}
    >
      <header
        style={{
          display: 'flex',
          alignItems: 'baseline',
          gap: 12,
          padding: '18px 24px',
          borderBottom: '1px solid var(--border)',
        }}
      >
        <h1
          className="mono"
          style={{ fontSize: 18, fontWeight: 600, letterSpacing: 0.2, margin: 0 }}
        >
          sakutei-view
        </h1>
        <span style={{ color: 'var(--text-muted)', fontSize: 13 }}>
          containerlab + Sakutei, one read-only view
        </span>
        {snapshot && (
          <span
            className="mono"
            style={{ marginLeft: 'auto', color: 'var(--text-faint)', fontSize: 12 }}
          >
            generated {new Date(snapshot.generated_at).toLocaleTimeString()}
          </span>
        )}
      </header>

      {snapshotError && (
        <div
          style={{
            margin: '16px 24px 0',
            padding: 12,
            border: '1px solid var(--status-down)',
            borderRadius: 4,
            color: 'var(--status-down)',
          }}
        >
          <strong>Couldn't reach the backend:</strong> {snapshotError}
        </div>
      )}

      <div className="layout" style={{ flex: 1 }}>
        <aside
          style={{
            background: 'var(--surface)',
            border: '1px solid var(--border)',
            borderRadius: 6,
            padding: 16,
          }}
        >
          {snapshot ? (
            <SourceHealthPanel health={snapshot.health} />
          ) : (
            <Loading />
          )}
        </aside>

        <main style={{ display: 'flex', flexDirection: 'column', gap: 28 }}>
          <section>
            <h2 style={sectionTitle}>Topology</h2>
            {snapshot ? (
              <TopologyDiagram nodes={snapshot.nodes} links={snapshot.links} />
            ) : (
              <Loading />
            )}
          </section>

          <section
            style={{
              background: 'var(--surface)',
              border: '1px solid var(--border)',
              borderRadius: 6,
              padding: 16,
              display: 'flex',
              flexDirection: 'column',
              gap: 20,
            }}
          >
            <div>
              <h2 style={sectionTitle}>Sakutei</h2>
              {snapshot ? (
                <SakuteiSummaryPanel
                  summary={snapshot.sakutei_summary}
                  stopReasons={snapshot.sakutei_stop_reasons}
                />
              ) : (
                <Loading />
              )}
            </div>

            <div style={{ borderTop: '1px solid var(--border)', paddingTop: 18 }}>
              <h3 style={subsectionTitle}>Dependency graph (waves)</h3>
              {snapshot ? (
                <DependencyWaves graph={snapshot.sakutei_graph} />
              ) : (
                <Loading />
              )}
            </div>
          </section>

          <section
            style={{
              background: 'var(--surface)',
              border: '1px solid var(--border)',
              borderRadius: 6,
              padding: 16,
            }}
          >
            <h2 style={sectionTitle}>Nodes</h2>
            {snapshot ? <NodeList nodes={snapshot.nodes} /> : <Loading />}
          </section>

          <section
            style={{
              background: 'var(--surface)',
              border: '1px solid var(--border)',
              borderRadius: 6,
              padding: 16,
            }}
          >
            <EventLog events={events} streamStatus={streamStatus} />
          </section>

          <details>
            <summary style={{ cursor: 'pointer', color: 'var(--text-muted)' }}>
              Raw snapshot JSON
            </summary>
            <pre
              className="mono"
              style={{
                marginTop: 10,
                padding: 12,
                background: 'var(--surface-raised)',
                border: '1px solid var(--border)',
                borderRadius: 6,
                overflowX: 'auto',
                fontSize: 12,
              }}
            >
              {snapshot ? JSON.stringify(snapshot, null, 2) : 'loading…'}
            </pre>
          </details>
        </main>
      </div>
    </div>
  )
}

const sectionTitle: CSSProperties = {
  fontSize: 15,
  fontWeight: 600,
  letterSpacing: 0.1,
  margin: '0 0 14px',
}

const subsectionTitle: CSSProperties = {
  fontSize: 13,
  fontWeight: 600,
  color: 'var(--text-muted)',
  margin: '0 0 12px',
}

function Loading() {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--text-muted)' }}>
      <StatusDot color="var(--status-drift)" />
      loading…
    </div>
  )
}
