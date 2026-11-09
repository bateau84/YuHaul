import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import * as api from '../api/client'
import { RouteEditor } from '../components/RouteEditor'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { useToast } from '../hooks/useToast'
import { shortRouteId } from '../lib/route'
import { simulationExplanation } from '../lib/outcome'
import type { Route, Run, RunStatus } from '../types'

// Persisted-status polling for the requested run (ARD-001): while the stored
// run is `running`, the client re-reads it. The start acknowledgement is never
// completion, and a failed status read never becomes a success.
export const RUN_STATUS_POLL_INTERVAL_MS = 5000

type RequestedRun = { runId: number; kind: 'backtest' | 'replay' }

function kindLabel(kind: RequestedRun['kind']): string {
  return kind === 'backtest' ? 'Backtest' : 'Replay'
}

// Status copy derived only from the persisted run record. "Completed" means
// the evaluation finished — a simulation never performed a playlist action
// (ARD-001). Detailed failure evidence stays owned by Run Detail.
function persistedStatusText(status: RunStatus): string {
  switch (status) {
    case 'running':
      return 'Evaluation in progress…'
    case 'completed':
      return 'Evaluation finished'
    case 'failed':
      return 'Failed'
    case 'interrupted':
      return 'Interrupted'
    default:
      return `Status: ${status}`
  }
}

// ADR-012 §1 / ARS-004: every route in a dry-run draft or backtest proposal is
// identified by its canonical UUIDv4. Numeric, missing, and malformed ids are
// invalid input, and one proposal must not repeat an id — the API rejects all
// of them with `invalid route id`. Check at upload time so the SPA never sends
// a route identity the API refuses, and so the failure names the offending
// entry instead of surfacing a bare 400 when the run starts. Uppercase hex is
// accepted here because the API normalizes it; ids are canonicalized on load.
const PROPOSAL_ROUTE_ID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i

function proposalRouteIdError(routes: Route[]): string | null {
  const seen = new Set<string>()
  for (let i = 0; i < routes.length; i++) {
    const id = typeof routes[i].id === 'string' ? routes[i].id : ''
    const label = `Route ${i + 1}${routes[i].name ? ` ("${routes[i].name}")` : ''}`
    if (!PROPOSAL_ROUTE_ID.test(id)) {
      return `${label} has no valid route id. Every proposed route needs its canonical UUIDv4 (for example 3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce); numeric route ids are not accepted.`
    }
    if (seen.has(id.toLowerCase())) {
      return `${label} repeats the route id of an earlier route in this file. Every proposed route needs its own UUID.`
    }
    seen.add(id.toLowerCase())
  }
  return null
}

function ScenarioDiff({ current, proposed }: { current: Route[]; proposed: Route[] }) {
  const currentById = new Map(current.map((r) => [r.id, r]))
  const proposedById = new Map(proposed.map((r) => [r.id, r]))
  const allIds = Array.from(new Set([...current.map((r) => r.id), ...proposed.map((r) => r.id)]))
  const rows = allIds
    .map((id) => {
      const oldRoute = currentById.get(id)
      const newRoute = proposedById.get(id)
      let status: 'added' | 'removed' | 'modified' | 'unchanged' = 'unchanged'
      if (!oldRoute) status = 'added'
      else if (!newRoute) status = 'removed'
      else if (JSON.stringify(oldRoute) !== JSON.stringify(newRoute)) status = 'modified'
      return { id, oldRoute, newRoute, status }
    })
    .filter((r) => r.status !== 'unchanged')

  if (rows.length === 0) {
    return <p style={{ color: 'var(--color-text-muted)' }}>Proposed configuration matches the current active configuration.</p>
  }

  return (
    <div style={{ display: 'grid', gap: 8 }}>
      {rows.map((row) => (
        <div
          key={row.id}
          style={{
            padding: 12,
            borderRadius: 8,
            border: '1px solid var(--color-border-default)',
            background:
              row.status === 'added'
                ? 'rgba(34, 197, 94, 0.08)'
                : row.status === 'removed'
                  ? 'rgba(239, 68, 68, 0.08)'
                  : 'rgba(245, 158, 11, 0.08)',
          }}
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <strong>
              {row.newRoute?.name ?? row.oldRoute?.name}{' '}
              <span className="route-short-id">({shortRouteId(row.id)})</span>
            </strong>
            <span className={`badge badge--${row.status === 'added' ? 'success' : row.status === 'removed' ? 'danger' : 'warning'}`}>
              {row.status}
            </span>
          </div>
          {row.status === 'modified' && row.oldRoute && row.newRoute && (
            <ul style={{ fontSize: '0.875rem', marginTop: 8 }}>
              {row.oldRoute.name !== row.newRoute.name && (
                <li>
                  Name: {row.oldRoute.name} → {row.newRoute.name}
                </li>
              )}
              {row.oldRoute.is_enabled !== row.newRoute.is_enabled && (
                <li>
                  Enabled: {String(row.oldRoute.is_enabled)} → {String(row.newRoute.is_enabled)}
                </li>
              )}
              {row.oldRoute.is_final !== row.newRoute.is_final && (
                <li>
                  Final: {String(row.oldRoute.is_final)} → {String(row.newRoute.is_final)}
                </li>
              )}
              {(row.oldRoute.conditions ?? []).length !== (row.newRoute.conditions ?? []).length && (
                <li>
                  Conditions: {(row.oldRoute.conditions ?? []).length} → {(row.newRoute.conditions ?? []).length}
                </li>
              )}
              {(row.oldRoute.actions ?? []).length !== (row.newRoute.actions ?? []).length && (
                <li>
                  Actions: {(row.oldRoute.actions ?? []).length} → {(row.newRoute.actions ?? []).length}
                </li>
              )}
              {(row.oldRoute.exclusions ?? []).length !== (row.newRoute.exclusions ?? []).length && (
                <li>
                  Exclusions: {(row.oldRoute.exclusions ?? []).length} → {(row.newRoute.exclusions ?? []).length}
                </li>
              )}
            </ul>
          )}
        </div>
      ))}
    </div>
  )
}

export function BacktestView() {
  const [mode, setMode] = useState<'hypothetical' | 'replay'>('hypothetical')
  const [inputMode, setInputMode] = useState<'editor' | 'upload'>('editor')
  const [activeRoutes, setActiveRoutes] = useState<Route[]>([])
  const [draft, setDraft] = useState<Route[]>([])
  const [routesState, setRoutesState] = useState<'loading' | 'ready' | 'error'>('loading')
  const [uploadError, setUploadError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [versions, setVersions] = useState<{ id: number; description?: string }[]>([])
  const [versionsError, setVersionsError] = useState(false)
  const [selectedVersion, setSelectedVersion] = useState<number | ''>('')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [running, setRunning] = useState(false)
  const [retrying, setRetrying] = useState(false)
  // The run requested by the last start acknowledgement, its latest persisted
  // status read, and whether that read failed (ARD-001): the persisted record
  // — never the start response — decides the displayed result.
  const [requested, setRequested] = useState<RequestedRun | null>(null)
  const [runDetail, setRunDetail] = useState<Run | null>(null)
  const [statusReadError, setStatusReadError] = useState(false)
  const [statusRetrying, setStatusRetrying] = useState(false)
  const requestedRef = useRef<RequestedRun | null>(null)
  const terminalToastedRef = useRef<number | null>(null)
  const statusRetryRef = useRef<HTMLButtonElement>(null)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const routesRetryRef = useRef<HTMLButtonElement>(null)
  const versionsRetryRef = useRef<HTMLButtonElement>(null)
  const recoveredRef = useRef(false)
  const { addToast } = useToast()

  useEffect(() => {
    let cancelled = false
    api
      .listRoutes()
      .then((r) => {
        if (cancelled) return
        setActiveRoutes(r)
        setDraft(JSON.parse(JSON.stringify(r)))
        setRoutesState('ready')
      })
      .catch(() => {
        if (cancelled) return
        setRoutesState('error')
      })
    api
      .listConfigVersions()
      .then((v) => {
        if (cancelled) return
        setVersions(v.map((x) => ({ id: x.id, description: x.change_description })))
        setVersionsError(false)
      })
      .catch(() => {
        if (cancelled) return
        setVersionsError(true)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Focus follows the failure: the Retry button of whichever failed region is
  // visible in the current mode, or the view heading once every failure has
  // recovered.
  useEffect(() => {
    if (retrying) return
    if (mode === 'hypothetical' && routesState === 'error') {
      routesRetryRef.current?.focus()
      return
    }
    if (mode === 'replay' && versionsError) {
      versionsRetryRef.current?.focus()
      return
    }
    if (routesState === 'error' || versionsError) return
    if (recoveredRef.current) {
      recoveredRef.current = false
      headingRef.current?.focus()
    }
  }, [mode, routesState, versionsError, retrying])

  async function handleRetry() {
    const routesAlreadyLoaded = routesState === 'ready'
    setRetrying(true)
    const [routesRes, versionsRes] = await Promise.allSettled([api.listRoutes(), api.listConfigVersions()])
    let failed = false
    if (routesRes.status === 'fulfilled') {
      setActiveRoutes(routesRes.value)
      // Keep edits already made in the editor; only seed the draft when the
      // routes were never loaded.
      if (!routesAlreadyLoaded) setDraft(JSON.parse(JSON.stringify(routesRes.value)))
      setRoutesState('ready')
    } else {
      setRoutesState('error')
      failed = true
    }
    if (versionsRes.status === 'fulfilled') {
      setVersions(versionsRes.value.map((x) => ({ id: x.id, description: x.change_description })))
      setVersionsError(false)
    } else {
      setVersionsError(true)
      failed = true
    }
    setRetrying(false)
    recoveredRef.current = !failed
    if (failed) addToast('Retry failed. Please try again.', 'error')
  }

  function handleFileUpload(e: React.ChangeEvent<HTMLInputElement>) {
    setUploadError(null)
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      try {
        const text = String(reader.result ?? '')
        const parsed = JSON.parse(text)
        const routes: Route[] = Array.isArray(parsed.routes) ? parsed.routes : Array.isArray(parsed) ? parsed : []
        if (routes.length === 0) {
          setUploadError('File did not contain any routes. Expected { routes: [...] } or an array of routes.')
          return
        }
        const idError = proposalRouteIdError(routes)
        if (idError) {
          // Leave the current draft untouched: an unusable file never replaces
          // the configuration the scenario diff is showing.
          setUploadError(idError)
          return
        }
        // Canonical lowercase so uploaded ids compare equal to the ids the API
        // emits in the scenario diff (ARS-004 output is always lowercase).
        setDraft(routes.map((r) => ({ ...r, id: r.id.toLowerCase() })))
        addToast(`Loaded ${routes.length} proposed routes from file`, 'success')
      } catch (err) {
        setUploadError(err instanceof Error ? err.message : 'Failed to parse uploaded file')
      }
    }
    reader.readAsText(file)
  }

  // The single persisted-status read used by the post-start read, the running
  // poll, and the status-error retry. A read that fails — including a missing
  // run record — leaves the result unknown; it is never converted into a
  // completion claim. A late answer for a run the user has already replaced
  // is ignored so it cannot commit stale state.
  const loadStatus = useCallback(
    (target: RequestedRun | null): Promise<void> => {
      if (!target) return Promise.resolve()
      return api.getRun(target.runId).then(
        (next) => {
          if (requestedRef.current?.runId !== target.runId) return
          if (next === undefined) {
            setStatusReadError(true)
            return
          }
          setStatusReadError(false)
          setRunDetail(next)
          if (next.status !== 'running' && terminalToastedRef.current !== target.runId) {
            terminalToastedRef.current = target.runId
            const completed = next.status === 'completed'
            addToast(
              completed
                ? `${kindLabel(target.kind)} evaluation finished`
                : `${kindLabel(target.kind)} run ${next.status}`,
              completed ? 'success' : 'error'
            )
          }
        },
        () => {
          if (requestedRef.current?.runId !== target.runId) return
          setStatusReadError(true)
        }
      )
    },
    [addToast]
  )

  // While the persisted status is running, keep re-reading it so a terminal
  // stored status replaces the active display without user action. A failed
  // background read surfaces its own alert and retry; it never clears the
  // last status that was actually read.
  const isPolling = requested !== null && runDetail?.status === 'running'
  useEffect(() => {
    if (!isPolling) return
    const timer = window.setInterval(() => {
      void loadStatus(requestedRef.current)
    }, RUN_STATUS_POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [isPolling, loadStatus])

  // A failed *first* status read leaves the result unknown: focus the retry so
  // the failure is reachable by keyboard. A failed background refresh keeps
  // focus where it is (same rule as the run-list refresh alert).
  useEffect(() => {
    if (statusReadError && !runDetail && !statusRetrying) statusRetryRef.current?.focus()
  }, [statusReadError, runDetail, statusRetrying])

  function handleStatusRetry() {
    setStatusRetrying(true)
    loadStatus(requestedRef.current).finally(() => setStatusRetrying(false))
  }

  async function run() {
    setRunning(true)
    // A new start supersedes any previously requested run: drop its result and
    // ignore its still-in-flight status reads.
    requestedRef.current = null
    terminalToastedRef.current = null
    setRequested(null)
    setRunDetail(null)
    setStatusReadError(false)
    try {
      const kind = mode === 'hypothetical' ? 'backtest' : 'replay'
      const res =
        kind === 'backtest'
          ? await api.startBacktest({ routes: draft, duplicate_threshold: 0.5 })
          : await api.startReplay(selectedVersion ? Number(selectedVersion) : undefined)
      // The start response is only an acknowledgement that a run was requested
      // (ARD-001). The run id is shown immediately; its persisted state —
      // never the ack — decides whether the result is running, finished,
      // failed, or unknown.
      const target: RequestedRun = { runId: res.run_id, kind }
      requestedRef.current = target
      setRequested(target)
      await loadStatus(target)
    } catch (err) {
      // A failed start fabricates no run id and no result.
      addToast(err instanceof Error ? err.message : 'Failed to start run', 'error')
    } finally {
      setRunning(false)
    }
  }

  return (
    <>
      <div className="page-header">
        <h1 className="page-title" ref={headingRef} tabIndex={-1}>
          Backtest & Replay
        </h1>
      </div>

      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }} role="tablist" aria-label="Backtest mode">
        <button
          className={`button ${mode === 'hypothetical' ? 'button--primary' : ''}`}
          onClick={() => setMode('hypothetical')}
          role="tab"
          aria-selected={mode === 'hypothetical'}
        >
          Hypothetical Backtest
        </button>
        <button
          className={`button ${mode === 'replay' ? 'button--primary' : ''}`}
          onClick={() => setMode('replay')}
          role="tab"
          aria-selected={mode === 'replay'}
        >
          Replay/Reprocess
        </button>
      </div>

      {mode === 'hypothetical' ? (
        routesState === 'error' ? (
          <div style={{ marginBottom: 16 }}>
            <FetchErrorRegion
              ref={routesRetryRef}
              message="Failed to load routes. Check your connection and try again."
              onRetry={handleRetry}
              retrying={retrying}
            />
          </div>
        ) : (
          <div className="card" style={{ marginBottom: 16 }}>
            <h2 className="section-title">Proposed configuration</h2>
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
              Edit routes below or upload a configuration file. Changes are not saved until applied.
            </p>
            <div style={{ display: 'flex', gap: 8, marginTop: 12, marginBottom: 12 }} role="tablist" aria-label="Configuration input method">
              <button
                className={`button ${inputMode === 'editor' ? 'button--primary' : ''}`}
                onClick={() => setInputMode('editor')}
                role="tab"
                aria-selected={inputMode === 'editor'}
              >
                Edit config
              </button>
              <button
                className={`button ${inputMode === 'upload' ? 'button--primary' : ''}`}
                onClick={() => setInputMode('upload')}
                role="tab"
                aria-selected={inputMode === 'upload'}
              >
                Upload config file
              </button>
            </div>

            {inputMode === 'upload' ? (
              <div>
                <input
                  ref={fileInputRef}
                  type="file"
                  accept=".json,application/json"
                  aria-label="Upload configuration file"
                  onChange={handleFileUpload}
                  style={{ marginBottom: 8 }}
                />
                {uploadError && <p style={{ color: 'var(--color-danger)' }}>{uploadError}</p>}
                <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                  Upload a JSON file containing routes. Supported shapes: an array of route objects or {'{ routes: [...] }'}.
                </p>
              </div>
            ) : routesState === 'loading' ? (
              <div className="empty-state">Loading routes...</div>
            ) : (
              draft.map((r, i) => (
                <div
                  key={r.id}
                  style={{ marginTop: 16, border: '1px solid var(--color-border-default)', borderRadius: 8, padding: 12 }}
                >
                  <strong>{r.name}</strong>
                  <RouteEditor
                    route={r}
                    onSave={(updated) => {
                      const next = [...draft]
                      next[i] = updated
                      setDraft(next)
                    }}
                    onCancel={() => {}}
                  />
                </div>
              ))
            )}
          </div>
        )
      ) : versionsError ? (
        <div style={{ marginBottom: 16 }}>
          <FetchErrorRegion
            ref={versionsRetryRef}
            message="Failed to load configuration versions. Check your connection and try again."
            onRetry={handleRetry}
            retrying={retrying}
          />
        </div>
      ) : (
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Select configuration version</h2>
          <select
            className="form-select"
            value={selectedVersion}
            onChange={(e) => setSelectedVersion(e.target.value === '' ? '' : Number(e.target.value))}
          >
            <option value="">Current active configuration</option>
            {versions.map((v) => (
              <option key={v.id} value={v.id}>
                Version {v.id} {v.description ? `- ${v.description}` : ''}
              </option>
            ))}
          </select>
        </div>
      )}

      {mode === 'hypothetical' && routesState === 'ready' && (
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Scenario diff</h2>
          <ScenarioDiff current={activeRoutes} proposed={draft} />
        </div>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Date range</h2>
        <div style={{ display: 'grid', gap: 12, gridTemplateColumns: '1fr 1fr' }}>
          <div>
            <label className="form-label" htmlFor="from-date">
              From date
            </label>
            <input id="from-date" type="date" className="form-input" value={dateFrom} onChange={(e) => setDateFrom(e.target.value)} />
          </div>
          <div>
            <label className="form-label" htmlFor="to-date">
              To date
            </label>
            <input id="to-date" type="date" className="form-input" value={dateTo} onChange={(e) => setDateTo(e.target.value)} />
          </div>
        </div>
      </div>

      <button className="button button--primary" onClick={run} disabled={running || (mode === 'hypothetical' && draft.length === 0)}>
        {running ? 'Starting…' : mode === 'hypothetical' ? 'Run backtest' : 'Run replay'}
      </button>

      {requested && (
        <div className="card" style={{ marginTop: 16 }}>
          <h2 className="section-title">Requested run</h2>
          {/* A failed first status read leaves the result unknown: same
              FetchErrorRegion recovery shape as Run Detail's initial-load
              failure (ARD-001) — never a completion claim. */}
          {statusReadError && !runDetail && (
            <FetchErrorRegion
              ref={statusRetryRef}
              message={`Could not read run #${requested.runId}'s persisted status. The result is unknown — it is not treated as completed.`}
              onRetry={handleStatusRetry}
              retrying={statusRetrying}
            />
          )}
          {/* A failed background refresh keeps the last actually-read status
              on screen and says it may be out of date, with a visible retry. */}
          {statusReadError && runDetail && (
            <div
              role="alert"
              aria-live="assertive"
              style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 12, marginBottom: 12 }}
            >
              <p style={{ margin: 0, color: 'var(--color-status-failed)' }}>
                Could not refresh run #{requested.runId}'s persisted status. The status shown may be out of date.
              </p>
              <button type="button" className="button" onClick={handleStatusRetry} disabled={statusRetrying}>
                {statusRetrying ? 'Retrying…' : 'Retry'}
              </button>
            </div>
          )}
          <p role="status" aria-live="polite" style={{ margin: '0 0 8px' }}>
            <strong>Run #{requested.runId}</strong> ({kindLabel(requested.kind)}) —{' '}
            <span>
              {runDetail
                ? persistedStatusText(runDetail.status)
                : statusReadError
                  ? 'Status unknown'
                  : 'Start accepted; reading persisted status…'}
            </span>
          </p>
          <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', margin: '0 0 12px' }}>
            {simulationExplanation}
          </p>
          {runDetail && (runDetail.status === 'failed' || runDetail.status === 'interrupted') && (
            <p style={{ color: 'var(--color-status-failed)', fontSize: '0.875rem', margin: '0 0 12px' }}>
              Open the run detail for the recorded reason and per-video evidence.
            </p>
          )}
          <Link to={`/runs/${requested.runId}`} className="button">
            View run #{requested.runId} detail
          </Link>
        </div>
      )}
    </>
  )
}
