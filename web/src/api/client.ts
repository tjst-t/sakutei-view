import type { LiveEvent, Snapshot } from './types'

/** Thrown by fetchSnapshot when the backend answers with a non-2xx status. */
export class SnapshotError extends Error {
  status: number

  constructor(status: number, body: string) {
    super(`fetching /api/snapshot failed: ${status} ${body}`.trim())
    this.name = 'SnapshotError'
    this.status = status
  }
}

/** Fetches the current snapshot from GET /api/snapshot. */
export async function fetchSnapshot(signal?: AbortSignal): Promise<Snapshot> {
  const res = await fetch('/api/snapshot', { signal })
  if (!res.ok) {
    throw new SnapshotError(res.status, await res.text())
  }
  return (await res.json()) as Snapshot
}

export type EventStreamStatus = 'connecting' | 'open' | 'closed'

/**
 * Opens GET /api/events and calls onEvent for each event the backend sends.
 * onStatusChange reports the EventSource's own connection lifecycle,
 * independent of per-source health (see Snapshot.health): the browser's
 * EventSource reconnects automatically on a dropped connection, same as
 * the backend's own reconnect-with-backoff for Sakutei/the plugin, so
 * "closed" here generally only means the page itself lost network access.
 *
 * Returns a cleanup function that closes the stream.
 */
export function subscribeEvents(
  onEvent: (event: LiveEvent) => void,
  onStatusChange?: (status: EventStreamStatus) => void,
): () => void {
  const source = new EventSource('/api/events')

  onStatusChange?.('connecting')
  source.onopen = () => onStatusChange?.('open')
  source.onerror = () => {
    // EventSource moves to CLOSED only when it gives up for good (e.g. the
    // server responded with a non-retryable status); a plain network drop
    // leaves readyState at CONNECTING while it retries on its own.
    if (source.readyState === EventSource.CLOSED) {
      onStatusChange?.('closed')
    }
  }
  source.onmessage = (ev) => {
    try {
      onEvent(JSON.parse(ev.data) as LiveEvent)
    } catch (err) {
      console.error('sakutei-view: could not parse event payload', err, ev.data)
    }
  }

  return () => source.close()
}
