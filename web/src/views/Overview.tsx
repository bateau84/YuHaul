import { useEffect, useState, useRef } from 'react'
import { Link } from 'react-router-dom'
import { Play, FlaskConical, AlertCircle } from 'lucide-react'
import * as api from '../api/client'
import { useToast } from '../hooks/useToast'
import { ConfirmDialog } from '../components/ConfirmDialog'
import type { Overview as OverviewData } from '../api/overview'

export function Overview() {
  const [overview, setOverview] = useState<OverviewData | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [retryKey, setRetryKey] = useState(0)
  const [confirmRun, setConfirmRun] = useState(false)
  const [confirmDry, setConfirmDry] = useState(false)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const { addToast } = useToast()

  useEffect(() => {
    let cancelled = false
    api.getOverview()
      .then((o) => {
        if (cancelled) return
        setOverview(o)
        setLoading(false)
      })
      .catch((err) => {
        if (cancelled) return
        setError(err instanceof Error ? err.message : 'Failed to load overview')
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [retryKey])

  function retry() {
    setLoading(true)
    setError(null)
    setRetryKey((k) => k + 1)
  }

  useEffect(() => {
    headingRef.current?.focus()
  }, [])

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
  if (error) {
    return (
      <div className="card" role="alert">
        <h2 className="section-title">Error loading overview</h2>
        <p>{error}</p>
        <button className="button button--primary" onClick={retry}>Retry</button>
      </div>
    )
  }
  if (!overview) return null

  const run = overview.last_run
  return (
    <>
      <div className="page-header">
        <h1 className="page-title" ref={headingRef} tabIndex={-1}>
          Overview
        </h1>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="button button--primary" onClick={() => setConfirmRun(true)}>
            <Play size={16} aria-hidden="true" /> Run Now
          </button>
          <button className="button" onClick={() => setConfirmDry(true)}>
            <FlaskConical size={16} aria-hidden="true" /> Dry Run
          </button>
        </div>
      </div>

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
