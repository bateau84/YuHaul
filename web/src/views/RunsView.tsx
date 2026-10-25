import { useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import * as api from '../api/client'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { useToast } from '../hooks/useToast'
import type { Run, RunStatus, RunType } from '../types'

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
  interrupted: 'Interrupted',
}

interface RunFilters {
  from: string
  to: string
  runType: RunType | ''
  status: RunStatus | ''
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
  const [filters, setFilters] = useState<RunFilters>(emptyFilters)
  const retryButtonRef = useRef<HTMLButtonElement>(null)
  const firstRowRef = useRef<HTMLTableRowElement>(null)
  const recoveredRef = useRef(false)
  const { addToast } = useToast()

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

  // Focus the Retry button whenever the view settles into the fetch-error
  // state: on first entry and again after a failed retry re-enables it.
  useEffect(() => {
    if (fetchError && !retrying) retryButtonRef.current?.focus()
  }, [fetchError, retrying])

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

  function updateFilter<K extends keyof RunFilters>(key: K, value: RunFilters[K]) {
    setFilters((prev) => ({ ...prev, [key]: value }))
  }

  const filtersActive =
    filters.from !== '' || filters.to !== '' || filters.runType !== '' || filters.status !== ''

  const visibleRuns = useMemo(() => {
    return runs.filter((r) => {
      if (filters.runType !== '' && r.run_type !== filters.runType) return false
      if (filters.status !== '' && r.status !== filters.status) return false
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
            {(Object.keys(runStatusLabels) as RunStatus[]).map((s) => (
              <option key={s} value={s}>
                {runStatusLabels[s]}
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
      ) : visibleRuns.length === 0 ? (
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
                <th scope="col">Videos</th>
                <th scope="col">Quota</th>
              </tr>
            </thead>
            <tbody>
              {visibleRuns.map((r, i) => (
                <tr key={r.id} ref={i === 0 ? firstRowRef : undefined} tabIndex={i === 0 ? -1 : undefined}>
                  <td data-label="Started">
                    <Link to={`/runs/${r.id}`}>{new Date(r.started_at).toLocaleString()}</Link>
                  </td>
                  <td data-label="Type">{runTypeLabels[r.run_type] ?? r.run_type}</td>
                  <td data-label="Status">{r.status}</td>
                  <td data-label="Videos">{r.videos_discovered}</td>
                  <td data-label="Quota">{r.quota_cost}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
