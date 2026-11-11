import { useEffect, useState, useRef } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Play, FlaskConical, AlertCircle } from 'lucide-react'
import * as api from '../api/client'
import { useToast } from '../hooks/useToast'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import type { Overview as OverviewData } from '../api/overview'
import { ApiError } from '../api/core'
import { StopRunControl, StopRunMessage, type StopOutcome } from '../components/StopRunControl'
import { DiscardRunControl } from '../components/DiscardRunControl'
import { canOfferStop, isStopping } from '../types'

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

// `resume_unavailable` reason sentences. The raw reason is never rendered:
// anything outside this closed set gets the "not available" sentence.
const resumeReasonSentences: Record<string, string> = {
  snapshot_unavailable: 'Its saved settings are missing.',
  snapshot_unreadable: 'Its saved settings cannot be read.',
  snapshot_inadmissible: 'Its saved settings cannot be used safely.',
  checkpoint_unrecognized: 'Its saved progress point is not recognized.',
  terminal_timestamp_contradiction: 'Its saved record is contradictory: it has a completion time but is marked interrupted.',
  recheck_at_write: 'Its saved state changed while Run Now was checking it. Check the run, then press Run Now again if that is still what you want.',
}
const resumeReasonFallback = 'The reason is not available in this version of the interface.'

// The five reasons that make the run discardable (design: "Where the control
// appears"). recheck_at_write and unknown reasons offer no button.
const discardableReasons = new Set([
  'snapshot_unavailable',
  'snapshot_unreadable',
  'snapshot_inadmissible',
  'checkpoint_unrecognized',
  'terminal_timestamp_contradiction',
])

function isDiscardableReason(reason?: string): boolean {
  return reason !== undefined && discardableReasons.has(reason)
}

function resumeRefusalPromise(reason: string | undefined, runId: number | undefined): string {
  const id = runId ?? 'it'
  if (isDiscardableReason(reason)) {
    return `While run ${id} is the oldest run stopped this way, Run Now will keep refusing. To unblock Run Now, you can discard run ${id}. The schedule is not affected.`
  }
  // The recheck_at_write sentence already says what to do; no refusal promise.
  if (reason === 'recheck_at_write') return ''
  return `While run ${id} is the oldest run stopped this way, Run Now may keep refusing. Open the run to see whether it can be discarded. The schedule is not affected.`
}

type RunNowNotice =
  | { kind: 'active' }
  | { kind: 'resume_unavailable'; runId?: number; reason?: string }
  | { kind: 'unavailable' }
  | { kind: 'unconfirmed' }

// Branches on HTTP status and the machine code only, never on message text.
function noticeForError(err: unknown): RunNowNotice {
  if (err instanceof ApiError) {
    if (err.status === 409 && err.code === 'production_run_active') return { kind: 'active' }
    if (err.status === 409 && err.code === 'resume_unavailable') return { kind: 'resume_unavailable', runId: err.runId, reason: err.reason }
    if (err.status === 503) return { kind: 'unavailable' }
  }
  return { kind: 'unconfirmed' }
}

function statusLabel(status: string): string {
  if (status === 'stopped') return 'Stopped by user'
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
  const [notice, setNotice] = useState<RunNowNotice | null>(null)
  const [noticeDiscarded, setNoticeDiscarded] = useState(false)
  const [statusStale, setStatusStale] = useState(false)
  const [statusCheckedAt, setStatusCheckedAt] = useState<Date | null>(null)
  const [stopOutcome, setStopOutcome] = useState<StopOutcome | null>(null)
  const stopMessageRef = useRef<HTMLDivElement>(null)
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

  // A run the server says is endable is saved as running but nothing executes
  // it: it is not "in progress", is not polled, and does not hold back Run Now.
  const lastRunActive = overview?.last_run?.status === 'running' && overview.last_run.endable !== true
  useEffect(() => {
    if (!lastRunActive) return
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
        if (next.last_run?.status !== 'running' || next.last_run.endable === true) setStartUncertain(false)
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
  }, [lastRunActive])

  const stopKind = stopOutcome?.kind
  useEffect(() => {
    if (stopKind === 'accepted' || stopKind === 'already_stopping') stopMessageRef.current?.focus()
  }, [stopKind])

  async function refreshOverview() {
    try {
      const next = await api.getOverview()
      setOverview(next)
      setStatusCheckedAt(new Date())
      setStatusStale(false)
    } catch {
      setStatusStale(true)
    }
  }

  async function handleStopOutcome(outcome: StopOutcome) {
    setStopOutcome(outcome)
    await refreshOverview()
  }

  function handleDiscarded() {
    setNoticeDiscarded(true)
    void refreshOverview()
  }

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
    setNotice(null)
    setNoticeDiscarded(false)
    setStartingRun(true)
    try {
      const res = await api.startRun()
      setStartUncertain(false)
      const admission = res.admission === 'started' || res.admission === 'resumed' ? res.admission : 'accepted'
      navigate(`/runs/${res.run_id}`, { state: { runNowAdmission: admission } })
    } catch (err) {
      const next = noticeForError(err)
      setNotice(next)
      // A refusal changed nothing, so Run Now stays usable. An unconfirmed
      // outcome keeps it disabled until the person has checked Runs.
      if (next.kind === 'unconfirmed') setStartUncertain(true)
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
        <button className="button button--primary" onClick={() => setConfirmRun(true)} disabled={fetchError || startingRun || lastRunActive || startUncertain}>
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
  const activeRun = run?.status === 'running' && run.endable !== true ? run : null
  const statusWord = activeRun && isStopping(activeRun) ? 'Stopping' : 'Running'
  return (
    <>
      {header}
      {notice && (
        <div className="card" role="alert" style={{ marginBottom: 16 }}>
          {notice.kind === 'active' && (
            <>
              <p style={{ margin: 0 }}><strong>Run Now did nothing.</strong> A production run is already running. Nothing was started or resumed.</p>
              <p style={{ margin: '8px 0 0' }}>Wait for it to finish, then press Run Now again.</p>
              <p style={{ margin: '8px 0 0' }}>
                {activeRun ? <Link to={`/runs/${activeRun.id}`}>View run</Link> : <Link to="/runs">View runs</Link>}
              </p>
            </>
          )}
          {notice.kind === 'resume_unavailable' && (
            <>
              <p style={{ margin: 0 }}>
                <strong>Run Now did nothing.</strong> Run {notice.runId ?? 'unknown'} stopped at its time limit but cannot be resumed safely. No new run was started and nothing was changed. {resumeReasonSentences[notice.reason ?? ''] ?? resumeReasonFallback}
              </p>
              {notice.runId !== undefined && (
                <p style={{ margin: '8px 0 0' }}><Link to={`/runs/${notice.runId}`}>View run {notice.runId}</Link></p>
              )}
              {!noticeDiscarded && (
                <p style={{ margin: '8px 0 0' }}>
                  {resumeRefusalPromise(notice.reason, notice.runId)} Dry Run stays available.
                </p>
              )}
              {notice.runId !== undefined && isDiscardableReason(notice.reason) && (
                <div style={{ margin: '8px 0 0' }}>
                  <DiscardRunControl
                    runId={notice.runId}
                    cause={notice.reason}
                    onDiscarded={handleDiscarded}
                    onRefused={refreshOverview}
                  />
                </div>
              )}
            </>
          )}
          {notice.kind === 'unavailable' && (
            <>
              <p style={{ margin: 0 }}><strong>Run Now did nothing.</strong> YuHaul can&apos;t accept a run right now. It may be starting up or shutting down. Nothing was started or resumed. Try again in a minute.</p>
              <p style={{ margin: '8px 0 0' }}><Link to="/runs">View runs</Link></p>
            </>
          )}
          {notice.kind === 'unconfirmed' && (
            <>
              <p style={{ margin: 0 }}>Could not confirm whether a run started or resumed. Check Runs before pressing Run Now again.</p>
              <p style={{ margin: '8px 0 0' }}><Link to="/runs">View runs</Link></p>
            </>
          )}
          {notice.kind !== 'unconfirmed' && (
            <p style={{ margin: '8px 0 0' }}>
              <button type="button" className="button" onClick={() => setNotice(null)}>Dismiss</button>
            </p>
          )}
        </div>
      )}
      {stopOutcome && (
        <StopRunMessage ref={stopMessageRef} outcome={stopOutcome} onDismiss={() => setStopOutcome(null)} />
      )}
      {(run || startingRun) && (
        <section className="card" role="region" aria-label="Latest run" aria-live="off" style={{ marginBottom: 16 }}>
          {startingRun ? (
            <p role="status" aria-live="polite" style={{ margin: 0 }}><strong>Starting run…</strong></p>
          ) : null}
          {activeRun && (
            <>
              {isStopping(activeRun) && <p style={{ margin: 0 }}>Run {activeRun.id} is stopping. Run Now stays unavailable until it has stopped.</p>}
              <p role="status" aria-live="polite" style={{ margin: 0 }}><strong>Run {activeRun.id} — {statusStale ? `Last known status: ${statusWord}` : statusWord}{phaseLabel(activeRun.current_phase) ? ` — ${statusStale ? 'Last known phase' : 'Current phase'}: ${phaseLabel(activeRun.current_phase)}` : ` — ${statusStale ? 'last known' : 'current'} phase unavailable.`}</strong></p>
              <p style={{ margin: '8px 0' }}>
                <Link to={`/runs/${activeRun.id}`}>View run</Link>
              </p>
              {canOfferStop(activeRun) && <StopRunControl run={activeRun} onOutcome={handleStopOutcome} />}
              {statusStale
                ? <p role="alert" aria-live="assertive" style={{ margin: 0 }}>Could not refresh run status; this run may still be processing.</p>
                : <p style={{ margin: 0 }}>Status checked at {statusCheckedAt?.toLocaleTimeString() ?? 'unknown'}.</p>}
            </>
          )}
          {run && !activeRun && (
            <>
              <p role="status" aria-live="polite" style={{ margin: 0 }}><strong>Run {run.id} — {run.run_type} — {run.status === 'running' && run.endable === true ? 'Marked as running — nothing is executing it' : statusLabel(run.status)}</strong></p>
              <p style={{ margin: '8px 0' }}><Link to={`/runs/${run.id}`}>View run</Link></p>
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
        title="Start a processing run now?"
        onConfirm={startRun}
        onCancel={() => setConfirmRun(false)}
      >
        This will discover new videos and apply your current routes. If an earlier Run Now run stopped at its time limit, this continues the oldest such run instead of starting a new one.
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
