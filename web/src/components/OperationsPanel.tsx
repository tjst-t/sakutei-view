import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError, fetchRun, fetchScripts, runAction } from '../api/client'
import { runIsInProgress, type PluginActionResult, type PluginRun, type PluginScript } from '../api/types'
import { runStatusColor, runStatusLabel } from './colors'
import { StatusDot } from './StatusDot'

// How often to poll GET /api/runs/{runID} while a run is in progress.
// Fast enough that "ずらす" → red node → "直す" feels live in a demo, slow
// enough not to hammer the plugin.
const RUN_POLL_INTERVAL_MS = 1200

/** Turns a plugin action id ("disturb") into an active-voice button label
 * ("Disturb"). Scripts only define machine-readable action ids, not their
 * own display labels, so this is the one place that formatting happens. */
function actionLabel(action: string): string {
  return action.charAt(0).toUpperCase() + action.slice(1)
}

type Phase = 'loading-scripts' | 'ready' | 'scripts-error'

/**
 * Lets a person run one of the device plugin's demo scripts ("ずらす" /
 * "直す" and whatever else the plugin defines) and watch it through to a
 * terminal state. Only ever rendered when Snapshot.actions_enabled is
 * true, so a deployment without a plugin and Basic auth configured never
 * shows a write control at all. Calls only this app's own
 * /api/scripts, /api/actions/*, /api/runs/* — never Sakutei's own
 * approval/write endpoints.
 */
export function OperationsPanel() {
  const [phase, setPhase] = useState<Phase>('loading-scripts')
  const [scripts, setScripts] = useState<PluginScript[]>([])
  const [scriptsError, setScriptsError] = useState<string | null>(null)
  const [selectedScriptId, setSelectedScriptId] = useState<string | null>(null)

  const [pendingAction, setPendingAction] = useState<string | null>(null)
  const [run, setRun] = useState<PluginRun | null>(null)
  const [runError, setRunError] = useState<string | null>(null)
  const [conflict, setConflict] = useState<PluginActionResult | null>(null)

  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null)

  useEffect(() => {
    let cancelled = false
    const controller = new AbortController()
    fetchScripts(controller.signal)
      .then((res) => {
        if (cancelled) return
        setScripts(res.scripts)
        setSelectedScriptId(res.scripts[0]?.id ?? null)
        setPhase('ready')
      })
      .catch((err) => {
        if (cancelled || controller.signal.aborted) return
        setScriptsError(err instanceof Error ? err.message : String(err))
        setPhase('scripts-error')
      })
    return () => {
      cancelled = true
      controller.abort()
    }
  }, [])

  // Stop polling on unmount, whatever state a run is in.
  useEffect(() => {
    return () => {
      if (pollRef.current) clearInterval(pollRef.current)
    }
  }, [])

  function stopPolling() {
    if (pollRef.current) {
      clearInterval(pollRef.current)
      pollRef.current = null
    }
  }

  async function pollRunOnce(runId: string): Promise<PluginRun | null> {
    try {
      const r = await fetchRun(runId)
      setRun(r)
      setRunError(null)
      return r
    } catch (err) {
      // A transient failure polling the run's status does not necessarily
      // mean the run itself failed — keep polling, but surface it.
      setRunError(err instanceof Error ? err.message : String(err))
      return null
    }
  }

  /** Fetches runId's status once immediately (so the panel shows something
   * the instant a run starts, rather than waiting a full poll interval),
   * then keeps polling until it reaches a terminal (non-"running") state. */
  async function pollRun(runId: string) {
    stopPolling()
    const first = await pollRunOnce(runId)
    if (first && !runIsInProgress(first.status)) return
    pollRef.current = setInterval(async () => {
      const r = await pollRunOnce(runId)
      if (r && !runIsInProgress(r.status)) stopPolling()
    }, RUN_POLL_INTERVAL_MS)
  }

  const selectedScript = useMemo(
    () => scripts.find((s) => s.id === selectedScriptId) ?? null,
    [scripts, selectedScriptId],
  )

  const busy = pendingAction !== null || (run !== null && runIsInProgress(run.status))

  async function handleRunAction(scriptId: string, action: string) {
    setPendingAction(action)
    setConflict(null)
    setRunError(null)
    try {
      const result = await runAction(scriptId, action)
      if (result.accepted && result.run_id) {
        await pollRun(result.run_id)
      } else {
        setConflict(result)
        // busy_by "run" means a run this plugin tracks holds the
        // exclusivity, and its run_id is included — track it too, so the
        // person can watch the run that is actually in progress finish,
        // rather than being left looking at a dead end. busy_by
        // "external" (or a v0.1 plugin's always-"run"-shaped 409 without a
        // run_id) has nothing further to poll.
        if (result.run_id) {
          await pollRun(result.run_id)
        }
      }
    } catch (err) {
      setRunError(err instanceof ApiError ? err.message : err instanceof Error ? err.message : String(err))
    } finally {
      setPendingAction(null)
    }
  }

  if (phase === 'loading-scripts') {
    return <p style={{ color: 'var(--text-muted)' }}>loading scripts…</p>
  }

  if (phase === 'scripts-error') {
    return (
      <p style={{ color: 'var(--status-down)' }}>
        Couldn't load scripts from the plugin: {scriptsError}
      </p>
    )
  }

  if (scripts.length === 0) {
    return <p style={{ color: 'var(--text-muted)' }}>The plugin has no demo scripts configured.</p>
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      {scripts.length > 1 && (
        <label style={{ display: 'flex', flexDirection: 'column', gap: 6, fontSize: 12, color: 'var(--text-muted)' }}>
          Script
          <select
            value={selectedScriptId ?? ''}
            onChange={(e) => {
              setSelectedScriptId(e.target.value)
              setConflict(null)
            }}
            style={{
              background: 'var(--surface-raised)',
              color: 'var(--text)',
              border: '1px solid var(--border)',
              borderRadius: 4,
              padding: '6px 8px',
              fontSize: 13,
            }}
          >
            {scripts.map((s) => (
              <option key={s.id} value={s.id}>
                {s.label}
              </option>
            ))}
          </select>
        </label>
      )}

      {selectedScript && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <div>
            <div style={{ fontSize: 13, fontWeight: 600 }}>{selectedScript.label}</div>
            {selectedScript.description && (
              <div style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 2 }}>
                {selectedScript.description}
              </div>
            )}
          </div>

          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {selectedScript.actions.map((action) => (
              <button
                key={action}
                type="button"
                disabled={busy}
                onClick={() => handleRunAction(selectedScript.id, action)}
                style={{
                  background: busy ? 'var(--surface-raised)' : 'var(--surface-raised)',
                  color: busy ? 'var(--text-faint)' : 'var(--text)',
                  border: '1px solid var(--border)',
                  borderRadius: 4,
                  padding: '8px 14px',
                  fontSize: 13,
                  fontWeight: 600,
                  cursor: busy ? 'not-allowed' : 'pointer',
                }}
              >
                {pendingAction === action ? 'Starting…' : actionLabel(action)}
              </button>
            ))}
          </div>
        </div>
      )}

      {conflict && (
        <div
          style={{
            display: 'flex',
            alignItems: 'flex-start',
            gap: 8,
            padding: 10,
            border: '1px solid var(--status-drift)',
            borderRadius: 4,
            fontSize: 12,
          }}
        >
          <span style={{ marginTop: 3 }}>
            <StatusDot color="var(--status-drift)" />
          </span>
          <span>
            <strong>Already running</strong> ({conflict.busy_by === 'external' ? 'outside this panel' : 'another run'})
            {conflict.reason ? `: ${conflict.reason}` : ''}
          </span>
        </div>
      )}

      {run && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: 10,
            background: 'var(--surface-raised)',
            border: '1px solid var(--border)',
            borderRadius: 4,
            fontSize: 12,
          }}
        >
          <StatusDot color={runStatusColor(run.status)} />
          <span className="mono" style={{ color: 'var(--text-muted)' }}>
            {run.action}
          </span>
          <span style={{ color: runStatusColor(run.status), fontWeight: 600 }}>
            {runStatusLabel(run.status)}
          </span>
          {run.message && <span style={{ color: 'var(--text-muted)' }}>— {run.message}</span>}
        </div>
      )}

      {runError && <p style={{ color: 'var(--status-down)', fontSize: 12 }}>{runError}</p>}
    </div>
  )
}
