import { useEffect, useState, useRef } from 'react'
import { Link } from 'react-router-dom'
import { Play, FlaskConical, AlertCircle } from 'lucide-react'
import * as api from '../api/client'
import { useToast } from '../hooks/useToast'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import type { Overview as OverviewData } from '../api/overview'

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

  useEffect(() => {
    let cancelled = false
    api.getOverview()
      .then((o) => {
        if (cancelled) return
        setOverview(o)
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
      setFetchError(false)
      recoveredRef.current = true
    } catch {
      addToast('Retry failed. Please try again.', 'error')
    } finally {
      setRetrying(false)
    }
  }

  async function startRun() {
    setConfirmRun(false)
    try {
      const res = await api.startRun()
      addToast(`Run ${res.run_id} started`, 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to start run', 'error')
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
        <button className="button button--primary" onClick={() => setConfirmRun(true)} disabled={fetchError}>
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
  return (
    <>
      {header}
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
