import { useEffect, useState, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Play, FlaskConical, AlertCircle } from 'lucide-react'
import * as api from '../api/client'
import { useToast } from '../hooks/useToast'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import type { Overview as OverviewData } from '../api/overview'
import { ApiError } from '../api/core'

const RUN_POLL_INTERVAL_MS = 5000

function phaseLabel(phase?: string): string | undefined {
  switch (phase) {
    case 'discovery': return 'Discovering videos'
    case 'exclusion': return 'Checking exclusions'
    case 'duplicate_detection': return 'Checking duplicates'
    case 'routing': return 'Evaluating routes'
    case 'action_execution': return 'Processing playlist actions'
    default: return undefined
  }
}

function statusLabel(status: string): string {
  return status.charAt(0).toUpperCase() + status.slice(1)
}

export function Overview() {
  const [overview, setOverview] = useState<OverviewData | null>(null)
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const [confirmRun, setConfirmRun] = useState(false)
  const [confirmDry, setConfirmDry] = useState(false)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const retryButtonRef = useRef<HTMLButtonElement>(null)
  const recoveredRef = useRef(false)
  const { addToast } = useToast()
  const navigate = useNavigate()
  const [startingRun, setStartingRun] = useState(false)
  const startingRunRef = useRef(false)
  const [startUncertain, setStartUncertain] = useState(false)
  const [statusStale, setStatusStale] = useState(false)
  const [statusCheckedAt, setStatusCheckedAt] = useState<Date | null>(null)
  const overviewRefreshInFlightRef = useRef(false)

  useEffect(() => {
    let cancelled = false
    api.getOverview()
      .then((o) => {
        if (cancelled) return
        setOverview(o)
        setStatusCheckedAt(new Date())
        setStatusStale(false)
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

  const lastRunStatus = overview?.last_run?.status
  useEffect(() => {
    if (lastRunStatus !== 'running') return
    let disposed = false
    const refresh = async () => {
      if (overviewRefreshInFlightRef.current) return
      overviewRefreshInFlightRef.current = true
      try {
        const next = await api.getOverview()
        if (disposed) return
        setOverview(next)
        setStatusCheckedAt(new Date())
        setStatusStale(false)
        if (next.last_run?.status !== 'running') setStartUncertain(false)
      } catch {
        if (!disposed) setStatusStale(true)
      } finally {
        overviewRefreshInFlightRef.current = false
      }
    }
    const timer = window.setInterval(refresh, RUN_POLL_INTERVAL_MS)
    const onVisible = () => { if (document.visibilityState === 'visible') void refresh() }
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      disposed = true
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', onVisible)
    }
  }, [lastRunStatus])

  // Focus the Retry button whenever the view settles into the fetch-error
  // state: on first entry and again after a failed retry re-enables it.
  useEffect(() => {
    if (fetchError && !retrying) retryButtonRef.current?.focus()
  }, [fetchError, retrying])

  // After a successful retry, move focus to the view heading.
  useEffect(() => {
    if (!recoveredRef.current || loading || fetchError) return
    recoveredRef.current = false
    headingRef.current?.focus()
  })

  useEffect(() => {
    headingRef.current?.focus()
  }, [])

  async function handleRetry() {
    setRetrying(true)
    try {
      const o = await api.getOverview()
      setOverview(o)
      setStatusCheckedAt(new Date())
      setStatusStale(false)
      setFetchError(false)
      recoveredRef.current = true
    } catch {
      addToast('Retry failed. Please try again.', 'error')
    } finally {
      setRetrying(false)
    }
  }

  async function startRun() {
    if (startingRunRef.current) return
    startingRunRef.current = true
    setConfirmRun(false)
    setStartingRun(true)
    try {
      const res = await api.startRun()
      setStartUncertain(false)
      navigate(`/runs/${res.run_id}`)
    } catch (err) {
      if (err instanceof ApiError) addToast(err.message, 'error')
      else setStartUncertain(true)
    } finally {
      startingRunRef.current = false
      setStartingRun(false)
    }
  }

  async function startDryRun() {
    setConfirmDry(false)
    try {
      const res = await api.startDryRun()
      addToast(`Dry run ${res.run_id} started`, 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to start dry run', 'error')
    }
  }

  if (loading) return <div className="empty-state">Loading overview...</div>

  const header = (
    <div className="page-header">
      <h1 className="page-title" ref={headingRef} tabIndex={-1}>
        Overview
      </h1>
      <div style={{ display: 'flex', gap: 8 }}>
        <button className="button button--primary" onClick={() => setConfirmRun(true)} disabled={fetchError || startingRun || Boolean(overview?.last_run?.status === 'running') || startUncertain}>
          <Play size={16} aria-hidden="true" /> Run Now
        </button>
        <button className="button" onClick={() => setConfirmDry(true)} disabled={fetchError}>
          <FlaskConical size={16} aria-hidden="true" /> Dry Run
        </button>
      </div>
    </div>
  )

  // Fetch-error replaces the dashboard body; the toolbar stays visible but
  // disabled until a retry succeeds.
  if (fetchError) {
    return (
      <>
        {header}
        <FetchErrorRegion
          ref={retryButtonRef}
          message="Failed to load overview. Check your connection and try again."
          onRetry={handleRetry}
          retrying={retrying}
        />
      </>
    )
  }

  if (!overview) return null

  const run = overview.last_run
  const activeRun = run?.status === 'running' ? run : null
  return (
    <>
      {header}
      {(run || startUncertain || startingRun) && (
        <section className="card" role="region" aria-label="Latest run" aria-live="off" style={{ marginBottom: 16 }}>
          {startingRun ? (
            <p role="status" aria-live="polite" style={{ margin: 0 }}><strong>Starting run…</strong></p>
          ) : null}
          {activeRun && (
            <>
              <p role="status" aria-live="polite" style={{ margin: 0 }}><strong>Run {activeRun.id} — {statusStale ? 'Last known status: Running' : 'Running'}{phaseLabel(activeRun.current_phase) ? ` — ${statusStale ? 'Last known phase' : 'Current phase'}: ${phaseLabel(activeRun.current_phase)}` : ` — ${statusStale ? 'last known' : 'current'} phase unavailable.`}</strong></p>
              <p style={{ margin: '8px 0' }}><Link to={`/runs/${activeRun.id}`}>View run</Link></p>
              {statusStale
                ? <p role="alert" aria-live="assertive" style={{ margin: 0 }}>Could not refresh run status; this run may still be processing.</p>
                : <p style={{ margin: 0 }}>Status checked at {statusCheckedAt?.toLocaleTimeString() ?? 'unknown'}.</p>}
            </>
          )}
          {run && !activeRun && (
            <>
              <p role="status" aria-live="polite" style={{ margin: 0 }}><strong>Run {run.id} — {run.run_type} — {statusLabel(run.status)}</strong></p>
              <p style={{ margin: '8px 0' }}><Link to={`/runs/${run.id}`}>View run</Link></p>
            </>
          )}
          {startUncertain && (
            <>
              <p role="status" aria-live="polite">Could not confirm whether the run started. Check Runs before starting another run.</p>
              <Link to="/runs">View runs</Link>
            </>
          )}
        </section>
      )}
      <div className="card-grid">
        <div className="card">
          <div className="metric-value">{run?.videos_discovered ?? 0}</div>
          <div className="metric-label">Discovered</div>
        </div>
        <div className="card">
          <div className="metric-value">{run?.videos_accepted ?? 0}</div>
          <div className="metric-label">Accepted</div>
        </div>
        <div className="card">
          <div className="metric-value">{run?.videos_deferred ?? 0}</div>
          <div className="metric-label">Deferred</div>
        </div>
        <div className="card">
          <div className="metric-value">{overview.error_count}</div>
          <div className="metric-label">Errors</div>
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Quota today</h2>
        <p>YouTube API quota used today—not run progress or playlist additions.</p>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 8 }}>
          <div style={{ flex: 1, height: 12, background: 'var(--color-surface-elevated)', borderRadius: 9999, overflow: 'hidden' }}>
            <div
              style={{
                width: `${Math.min(100, (overview.quota.consumed_today / overview.quota.daily_limit) * 100)}%`,
                height: '100%',
                background: overview.quota.remaining === 0 ? 'var(--color-status-deferred)' : 'var(--color-interactive-primary)',
              }}
            />
          </div>
          <span style={{ fontWeight: 600 }}>
            {overview.quota.consumed_today} / {overview.quota.daily_limit}
          </span>
        </div>
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
          Resets at {overview.quota.reset_at ? new Date(overview.quota.reset_at).toLocaleString() : 'unknown'}
        </p>
      </div>

      <div className="card">
        <h2 className="section-title">Recent runs</h2>
        {!run || overview.runs_today === 0 ? (
          <div className="empty-state">No runs yet.</div>
        ) : (
          <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
            <li style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 0', borderBottom: '1px solid var(--color-border-default)' }}>
              <Link to={`/runs/${run.id}`}>
                {new Date(run.started_at).toLocaleString()} — {run.run_type}
              </Link>
              <span>{run.videos_discovered} videos</span>
            </li>
          </ul>
        )}
        {overview.error_count > 0 && (
          <div style={{ marginTop: 16 }}>
            <Link to="/errors" className="button" style={{ width: 'auto' }}>
              <AlertCircle size={16} /> View {overview.error_count} error{overview.error_count === 1 ? '' : 's'}
            </Link>
          </div>
        )}
      </div>

      <ConfirmDialog
        open={confirmRun}
        title="Start a processing run?"
        onConfirm={startRun}
        onCancel={() => setConfirmRun(false)}
      >
        This will discover new videos and apply your current routes.
      </ConfirmDialog>

      <ConfirmDialog
        open={confirmDry}
        title="Start a dry run?"
        onConfirm={startDryRun}
        onCancel={() => setConfirmDry(false)}
      >
        This will evaluate pending videos without adding anything to playlists.
      </ConfirmDialog>
    </>
  )
}
