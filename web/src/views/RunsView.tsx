import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import * as api from '../api/client'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { StopRunControl, StopRunMessage, type StopOutcome } from '../components/StopRunControl'
import { useToast } from '../hooks/useToast'
import { isStopping } from '../types'
import type { Run, RunStatus, RunType } from '../types'

// Persisted-state polling interval (ARD-001: while a listed run is stored as
// running, the client re-reads stored run state; start acknowledgement is
// never treated as completion).
export const RUN_POLL_INTERVAL_MS = 5000

const runTypeLabels: Record<RunType, string> = {
  scheduled: 'Scheduled',
  manual: 'Manual',
  dry_run: 'Dry Run',
  replay: 'Replay',
  backtest: 'Backtest',
}

const runStatusLabels: Record<RunStatus, string> = {
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
  interrupted: 'Interrupted',
  stopped: 'Stopped by user',
}

// "Stopping" is not a stored status: it is a running run with a recorded stop
// (isStopping). It is offered as its own filter value.
type StatusFilter = RunStatus | 'stopping'

const statusFilterLabels: Record<StatusFilter, string> = {
  ...runStatusLabels,
  stopping: 'Stopping',
}

// A stopping run is still running, so it matches both the Stopping and the
// Running filters. A stopped run matches only Stopped by user: it is neither
// Failed nor Completed.
function matchesStatus(run: Run, filter: StatusFilter | ''): boolean {
  if (filter === '') return true
  if (filter === 'stopping') return isStopping(run)
  return run.status === filter
}

function StopSquareIcon() {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true" focusable="false">
      <rect x="1" y="1" width="8" height="8" rx="1" fill="currentColor" />
    </svg>
  )
}

// Neutral badge with icon and text: never colour alone, and not styled as a
// failure.
function StatusCell({ run }: { run: Run }) {
  if (isStopping(run) || run.status === 'stopped') {
    return (
      <span className="badge badge--not-evaluated">
        <StopSquareIcon />
        {isStopping(run) ? 'Stopping' : runStatusLabels.stopped}
      </span>
    )
  }
  return <>{runStatusLabels[run.status] ?? run.status}</>
}

// Selected shows "Unknown" for a null/absent count (never captured) and "0"
// only for a captured empty selection (BR-020).
function selectedCountLabel(run: Run): string {
  return run.videos_selected == null ? 'Unknown' : String(run.videos_selected)
}

interface RunFilters {
  from: string
  to: string
  runType: RunType | ''
  status: StatusFilter | ''
}

const emptyFilters: RunFilters = { from: '', to: '', runType: '', status: '' }

// UTC calendar day of a run start, matching the UTC from/to bounds the
// analytics range picker already uses.
function utcDay(iso: string): string {
  return new Date(iso).toISOString().slice(0, 10)
}

export function RunsView() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const [refreshError, setRefreshError] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [filters, setFilters] = useState<RunFilters>(emptyFilters)
  const retryButtonRef = useRef<HTMLButtonElement>(null)
  const firstRowRef = useRef<HTMLTableRowElement>(null)
  const recoveredRef = useRef(false)
  const { addToast } = useToast()
  const [stopOutcome, setStopOutcome] = useState<StopOutcome | null>(null)
  const stopMessageRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    let cancelled = false
    api.listRuns()
      .then((r) => {
        if (cancelled) return
        setRuns(r)
        setFetchError(false)
        setLoading(false)
      })
      .catch(() => {
        if (cancelled) return
        setFetchError(true)
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Persisted-state refresh: while any listed run is stored as running, keep
  // re-reading the run list so a terminal status replaces "running" without
  // user action (ARD-001). A failed background read never clears the list it
  // could not refresh; it surfaces a visible retry instead of implying the
  // data is current.
  const hasRunning = runs.some((r) => r.status === 'running')
  useEffect(() => {
    if (!hasRunning || fetchError) return
    let cancelled = false
    const timer = window.setInterval(() => {
      api.listRuns()
        .then((r) => {
          if (cancelled) return
          setRuns(r)
          setRefreshError(false)
        })
        .catch(() => {
          if (cancelled) return
          setRefreshError(true)
        })
    }, RUN_POLL_INTERVAL_MS)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [hasRunning, fetchError])

  // Focus the Retry button whenever the view settles into the fetch-error
  // state: on first entry and again after a failed retry re-enables it.
  useEffect(() => {
    if (fetchError && !retrying) retryButtonRef.current?.focus()
  }, [fetchError, retrying])

  // A background refresh failure renders its own alert with a Retry control
  // (see refreshErrorRegion) and deliberately does not move focus; the
  // fetch-error region above keeps its focus-on-entry behavior for full load
  // failures.

  // After a successful retry, move focus to the first run row.
  useEffect(() => {
    if (!recoveredRef.current || loading || fetchError) return
    recoveredRef.current = false
    firstRowRef.current?.focus()
  })

  async function handleRetry() {
    setRetrying(true)
    try {
      const r = await api.listRuns()
      setRuns(r)
      setFetchError(false)
      recoveredRef.current = true
    } catch {
      addToast('Retry failed. Please try again.', 'error')
    } finally {
      setRetrying(false)
    }
  }

  async function handleRefreshRetry() {
    setRefreshing(true)
    try {
      const r = await api.listRuns()
      setRuns(r)
      setRefreshError(false)
    } catch {
      addToast('Refresh failed. Please try again.', 'error')
    } finally {
      setRefreshing(false)
    }
  }

  // Acknowledgements take focus (the Stop button was replaced); refusals do not.
  const stopKind = stopOutcome?.kind
  useEffect(() => {
    if (stopKind === 'accepted' || stopKind === 'already_stopping') stopMessageRef.current?.focus()
  }, [stopKind])

  // Re-read the list so the row shows the saved status, not the press.
  async function handleStopOutcome(outcome: StopOutcome) {
    setStopOutcome(outcome)
    try {
      setRuns(await api.listRuns())
      setRefreshError(false)
    } catch {
      setRefreshError(true)
    }
  }

  function updateFilter<K extends keyof RunFilters>(key: K, value: RunFilters[K]) {
    setFilters((prev) => ({ ...prev, [key]: value }))
  }

  const filtersActive =
    filters.from !== '' || filters.to !== '' || filters.runType !== '' || filters.status !== ''

  const visibleRuns = useMemo(() => {
    return runs.filter((r) => {
      if (filters.runType !== '' && r.run_type !== filters.runType) return false
      if (!matchesStatus(r, filters.status)) return false
      const day = utcDay(r.started_at)
      if (filters.from !== '' && day < filters.from) return false
      if (filters.to !== '' && day > filters.to) return false
      return true
    })
  }, [runs, filters])

  if (loading) return <div className="empty-state">Loading runs...</div>

  // The filter region stays visible during a fetch failure, with its controls
  // disabled until a retry succeeds (amendment: View: Run List, fetch-error).
  const filterRegion = (
    <div className="card" style={{ marginBottom: 16 }}>
      <div style={{ display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))' }}>
        <div>
          <label className="form-label" htmlFor="runs-filter-from">
            From date
          </label>
          <input
            id="runs-filter-from"
            type="date"
            className="form-input"
            value={filters.from}
            disabled={fetchError}
            onChange={(e) => updateFilter('from', e.target.value)}
          />
        </div>
        <div>
          <label className="form-label" htmlFor="runs-filter-to">
            To date
          </label>
          <input
            id="runs-filter-to"
            type="date"
            className="form-input"
            value={filters.to}
            disabled={fetchError}
            onChange={(e) => updateFilter('to', e.target.value)}
          />
        </div>
        <div>
          <label className="form-label" htmlFor="runs-filter-type">
            Run type
          </label>
          <select
            id="runs-filter-type"
            className="form-select"
            value={filters.runType}
            disabled={fetchError}
            onChange={(e) => updateFilter('runType', e.target.value as RunFilters['runType'])}
          >
            <option value="">All run types</option>
            {(Object.keys(runTypeLabels) as RunType[]).map((t) => (
              <option key={t} value={t}>
                {runTypeLabels[t]}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label className="form-label" htmlFor="runs-filter-status">
            Status
          </label>
          <select
            id="runs-filter-status"
            className="form-select"
            value={filters.status}
            disabled={fetchError}
            onChange={(e) => updateFilter('status', e.target.value as RunFilters['status'])}
          >
            <option value="">All statuses</option>
            {(Object.keys(statusFilterLabels) as StatusFilter[]).map((s) => (
              <option key={s} value={s}>
                {statusFilterLabels[s]}
              </option>
            ))}
          </select>
        </div>
      </div>
      <p style={{ margin: '8px 0 0', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
        Format: YYYY-MM-DD
      </p>
    </div>
  )

  const refreshErrorRegion = refreshError && (
    <div
      className="card"
      role="alert"
      aria-live="assertive"
      style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 12, marginBottom: 16 }}
    >
      <p style={{ margin: 0, display: 'flex', alignItems: 'center', gap: 8, color: 'var(--color-status-failed)' }}>
        Could not refresh run status. The list below may be out of date.
      </p>
      <button
        type="button"
        className="button"
        onClick={handleRefreshRetry}
        disabled={refreshing}
      >
        {refreshing ? 'Retrying…' : 'Retry'}
      </button>
    </div>
  )

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Runs</h1>
      </div>
      {filterRegion}
      {fetchError ? (
        <FetchErrorRegion
          ref={retryButtonRef}
          message="Failed to load runs. Check your connection and try again."
          onRetry={handleRetry}
          retrying={retrying}
        />
      ) : (
        <>
          {refreshErrorRegion}
          {stopOutcome && (
            <StopRunMessage ref={stopMessageRef} outcome={stopOutcome} onDismiss={() => setStopOutcome(null)} />
          )}
          {visibleRuns.length === 0 ? (
            <div className="card empty-state">
              {filtersActive
                ? 'No runs match the current filters.'
                : 'No runs yet. Start a processing run to see results here.'}
            </div>
          ) : (
            <div className="card">
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Started</th>
                    <th scope="col">Type</th>
                    <th scope="col">Status</th>
                    <th scope="col">Selected</th>
                    <th scope="col">Evaluated</th>
                    <th scope="col">Discovered</th>
                    <th scope="col">Quota</th>
                    <th scope="col">Actions</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleRuns.map((r, i) => (
                    <tr key={r.id} ref={i === 0 ? firstRowRef : undefined} tabIndex={i === 0 ? -1 : undefined}>
                      <td data-label="Started">
                        <Link to={`/runs/${r.id}`}>{new Date(r.started_at).toLocaleString()}</Link>
                      </td>
                      <td data-label="Type">{runTypeLabels[r.run_type] ?? r.run_type}</td>
                      <td data-label="Status"><StatusCell run={r} /></td>
                      <td data-label="Selected">{selectedCountLabel(r)}</td>
                      <td data-label="Evaluated">{r.videos_evaluated ?? '—'}</td>
                      <td data-label="Discovered">{r.videos_discovered}</td>
                      <td data-label="Quota">{r.quota_cost}</td>
                      {/* Below 768px rows stack full width, so the 44px Stop
                          button wraps inside the row at 320px with no clipping. */}
                      <td data-label={r.status === 'running' ? 'Actions' : undefined}>
                        {r.status === 'running' && <StopRunControl run={r} onOutcome={handleStopOutcome} />}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </>
      )}
    </>
  )
}
