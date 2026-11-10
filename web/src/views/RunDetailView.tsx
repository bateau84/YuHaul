import { useCallback, useEffect, useRef, useState } from 'react'
import { useParams, Link, useNavigate, useLocation } from 'react-router-dom'
import * as api from '../api/client'
import { Badge } from '../components/Badge'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { CausalExclusion } from '../components/CausalExclusion'
import { StopRunControl, StopRunMessage, type StopOutcome } from '../components/StopRunControl'
import { useToast } from '../hooks/useToast'
import { deriveRouteDecision, simulationExplanation, simulationMode, videoOutcomeLabels } from '../lib/outcome'
import { routeAttribution } from '../lib/route-attribution'
import { currentLocalObservedAt, currentLocalUnavailableLabel } from '../lib/current-local-metadata'
import { isStopping, type PublishedRange, type Run, type RunStatus, type VideoFinalOutcome, type VideoOutcome } from '../types'

// The run videos endpoint caps page_size at 100 (internal/server/run_handlers.go).
// Request the maximum so one page holds as much of the run as possible; later
// pages are reached through the existing `page` parameter (ARS-001), so every
// video outcome of a large run stays inspectable.
const VIDEO_PAGE_SIZE = 100

// Persisted-status polling while the stored run status is `running`. SSE is
// best-effort, so the terminal display always follows the stored state
// (ARD-001): start acknowledgement and events never establish completion.
export const RUN_POLL_INTERVAL_MS = 5000

const runStatusLabels: Record<RunStatus, string> = {
  running: 'Running',
  completed: 'Completed',
  failed: 'Failed',
  interrupted: 'Interrupted',
  stopped: 'Stopped by user',
}

// Selected shows "Unknown" for a null/absent count (never captured) and "0"
// only for a captured empty selection (BR-020). Kept in step with RunsView.
function selectedCountLabel(run: Run): string {
  return run.videos_selected == null ? 'Unknown' : String(run.videos_selected)
}

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

function progressAnnouncement(run: Run, readError: boolean): string {
  const label = phaseLabel(run.current_phase)
  if (isStopping(run)) {
    return label ? `Stopping — ${label}. Run ${run.id} has been told to stop and is saving its work.` : `Stopping. Run ${run.id} has been told to stop and is saving its work.`
  }
  if (run.status === 'running') {
    const phasePrefix = readError ? 'Last known phase' : 'Current phase'
    return label ? `Run in progress — ${phasePrefix}: ${label}` : 'Run in progress — Current phase unavailable.'
  }
  return label ? `Last recorded phase: ${label}` : 'Last recorded phase unavailable.'
}

// Run-level failure text (ARD-001), independent of the Errors queue. The
// server only sends `interrupt_reason` when the stored text passed safe
// vetting; when it was withheld, `failure_reason_unavailable` marks a reason
// as existing but not displayable. Missing evidence never invents a reason.
function failureExplanation(run: Run): string {
  if (run.failure_reason_unavailable) {
    return 'A reason was recorded for this run, but it cannot be displayed safely.'
  }
  if (run.interrupt_reason) return `Reason: ${run.interrupt_reason}`
  return run.status === 'failed'
    ? 'No failure reason was recorded for this run.'
    : 'No interruption reason was recorded for this run.'
}

function terminalStatusLine(status: RunStatus): string {
  switch (status) {
    case 'stopped':
      return 'Run stopped by user'
    case 'completed':
      return 'Run completed'
    case 'failed':
      return 'Run failed'
    case 'interrupted':
      return 'Run interrupted'
    default:
      // Unknown/running statuses never claim a terminal result; the raw value
      // is shown instead of crashing on an unmapped label.
      return `Run status: ${runStatusLabels[status] ?? status}`
  }
}

type RunNowAdmission = 'started' | 'resumed' | 'accepted'

// The acknowledgement line of this Run Now press only. It comes from
// navigation state, because the server keeps no "was resumed" fact.
function admissionFromState(state: unknown): RunNowAdmission | null {
  const value = (state as { runNowAdmission?: unknown } | null)?.runNowAdmission
  return value === 'started' || value === 'resumed' || value === 'accepted' ? value : null
}

function admissionLine(admission: RunNowAdmission, runId: number): string {
  switch (admission) {
    case 'resumed': return `Run ${runId} resumed. It continues from where it stopped. No new run was started.`
    case 'started': return `Run ${runId} started.`
    default: return `Run ${runId} accepted.`
  }
}

// A manual Run Now run the server says stopped at its time limit. Anything
// else (shutdown, unknown cause, scheduled, legacy, failed, simulation) keeps
// the generic interrupted copy and makes no resume promise.
function stoppedAtTimeLimit(run: Run): boolean {
  return run.run_type === 'manual' && run.status === 'interrupted'
    && run.initiated_by === 'run_now' && run.interrupt_cause === 'time_limit'
}

function statusText(run: Run): string {
  return isStopping(run) ? 'Stopping' : run.status
}

const outcomeOptions = Object.entries(videoOutcomeLabels) as [VideoFinalOutcome, string][]

function currentMetadata(value: VideoOutcome['current_local_metadata'], field: 'title' | 'channel_name' | 'duration_seconds'): string {
  const availability = field === 'duration_seconds'
    ? value?.duration_availability
    : value?.[`${field}_availability`]
  if (availability === 'current_local' && value?.[field] != null) {
    return field === 'duration_seconds'
      ? value.duration_seconds === 0 ? '0 seconds' : `${value.duration_seconds} seconds`
      : String(value[field])
  }
  return currentLocalUnavailableLabel(field, availability)
}

// The range saved with a Backtest/Replay run. Days are shown exactly as stored
// (UTC calendar days); a missing bound is "none".
function describePublishedRange(range: PublishedRange): string {
  if (range.from === null && range.to === null) return 'Published date filter: none. No date filter was applied.'
  return `Published date filter (UTC days, both included): from ${range.from ?? 'none'} to ${range.to ?? 'none'}.`
}

export function RunDetailView() {
  const { id } = useParams<{ id: string }>()
  return <RunDetailContent key={id} id={id} />
}

function RunDetailContent({ id }: { id?: string }) {
  const navigate = useNavigate()
  const { addToast } = useToast()
  const location = useLocation()
  const runId = Number(id)
  // Read once, then drop it from history state so a reload shows ordinary
  // saved status without the transient line.
  const [admission] = useState(() => admissionFromState(location.state))
  const [run, setRun] = useState<Run | null>(null)
  const [runNotFound, setRunNotFound] = useState(false)
  const [runReadError, setRunReadError] = useState(false)
  const [runContextError, setRunContextError] = useState(false)
  const [runRetrying, setRunRetrying] = useState(false)
  const [stopOutcome, setStopOutcome] = useState<StopOutcome | null>(null)
  const stopMessageRef = useRef<HTMLDivElement>(null)
  const [statusCheckedAt, setStatusCheckedAt] = useState<Date | null>(null)
  const [videos, setVideos] = useState<VideoOutcome[]>([])
  const [videoTotal, setVideoTotal] = useState(0)
  const [videoPageCount, setVideoPageCount] = useState(1)
  const [page, setPage] = useState(1)
  const [videosError, setVideosError] = useState(false)
  const [videosLoading, setVideosLoading] = useState(true)
  const [outcomeFilter, setOutcomeFilter] = useState<VideoFinalOutcome | ''>('')
  const headingRef = useRef<HTMLHeadingElement>(null)
  const statusRetryRef = useRef<HTMLButtonElement>(null)
  const runStatusRef = useRef<RunStatus | undefined>(undefined)
  const runReadRequestRef = useRef(0)
  const runReadInFlightRef = useRef<Promise<void> | null>(null)
  const loaded = run !== null

  useEffect(() => {
    if (admission) navigate(location.pathname + location.search, { replace: true, state: null })
    // Run once on arrival.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Focus the run heading once the run has loaded (or re-loaded through a
  // status retry).
  useEffect(() => {
    if (loaded) headingRef.current?.focus()
  }, [loaded])

  // A failed *initial* status read leaves the run state unknown: show the
  // retry region instead of "Loading run..." forever, and never imply a
  // terminal result (Task 2.1 criteria).
  useEffect(() => {
    if ((runReadError || runContextError) && !run && !runRetrying) statusRetryRef.current?.focus()
  }, [runReadError, runContextError, run, runRetrying])

  // BR-032 AC5: the outcome filter is applied server-side, so the empty state
  // and the shown total reflect the whole run for the selected category, not
  // just a pre-filtered page. Only the latest request may update the list, so
  // a slow response for an earlier filter selection cannot overwrite it.
  const videosRequestRef = useRef(0)
  const loadVideos = useCallback(() => {
    const requestId = ++videosRequestRef.current
    setVideosLoading(true)
    return api
      .getRunVideos(runId, { outcome: outcomeFilter || undefined, pageSize: VIDEO_PAGE_SIZE, page })
      .then((result) => {
        if (requestId !== videosRequestRef.current) return
        const totalPages = Math.max(1, result.totalPages ?? 1)
        // The current page can fall outside the server-side page count (the
        // run changed under us). Clamp to the last available page instead of
        // showing a false empty list; the `totalPages < page` guard keeps a
        // contradictory server answer from looping.
        if (result.videos.length === 0 && page > 1 && totalPages < page) {
          setPage(totalPages)
          return
        }
        setVideos(result.videos)
        setVideoTotal(result.total)
        setVideoPageCount(totalPages)
        setVideosError(false)
        setVideosLoading(false)
      })
      .catch(() => {
        if (requestId !== videosRequestRef.current) return
        setVideos([])
        setVideoTotal(0)
        setVideosError(true)
        setVideosLoading(false)
      })
  }, [runId, outcomeFilter, page])

  useEffect(() => {
    loadVideos()
  }, [loadVideos])

  // Refresh the videos through the current filter/page without tearing down
  // the run-completion subscription on every filter change.
  const loadVideosRef = useRef(loadVideos)
  useEffect(() => {
    loadVideosRef.current = loadVideos
  }, [loadVideos])

  // The single persisted-state read used by initial load, retries, the
  // running-status poll, and visibility refreshes. Terminal transitions
  // observed through this read reload the video evidence and toast success
  // only when the *stored* status is completed (ARD-001: a status-read error
  // is never converted into a success claim). State updates live in the
  // promise handlers, so nothing runs synchronously inside the caller effect.
  const loadRun = useCallback((): Promise<void> => {
    // Poll, visibility refresh, and explicit retry share one read. A newer
    // request must not invalidate a successful terminal response that is
    // still in flight (the API can take longer than the poll interval).
    if (runReadInFlightRef.current) return runReadInFlightRef.current
    const requestId = ++runReadRequestRef.current
    const request = api.getRun(runId).then(
      (next) => {
        if (requestId !== runReadRequestRef.current) return
        setRunReadError(false)
        if (next === undefined) {
          setRunContextError(false)
          setRunNotFound(true)
          setRun(null)
          runStatusRef.current = undefined
          return
        }
        setRunNotFound(false)
        if (!['manual', 'scheduled', 'dry_run', 'replay', 'backtest'].includes(next.run_type)) {
          setRunContextError(true)
          setRun(null)
          runStatusRef.current = undefined
          return
        }
        setRunContextError(false)
        const previous = runStatusRef.current
        if (previous === 'completed' || previous === 'failed' || previous === 'stopped') return
        runStatusRef.current = next.status
        setRun(next)
        setStatusCheckedAt(new Date())
        if (previous === 'running' && next.status !== 'running') {
          loadVideosRef.current()
        }
        if (previous === 'running' && next.status === 'completed') {
          addToast('Run completed', 'success')
        }
      },
      () => {
        if (requestId !== runReadRequestRef.current) return
        setRunReadError(true)
      },
    ).finally(() => {
      if (runReadInFlightRef.current === request) runReadInFlightRef.current = null
    })
    runReadInFlightRef.current = request
    return request
  }, [runId, addToast])

  useEffect(() => {
    void loadRun()
  }, [loadRun])

  async function handleRunRetry() {
    setRunRetrying(true)
    setRunContextError(false)
    try {
      await loadRun()
    } finally {
      setRunRetrying(false)
    }
  }

  // While the stored status is running, keep re-reading it so a terminal
  // stored status replaces the progress display without user action.
  const runStatus = run?.status
  useEffect(() => {
    if (runNotFound || runStatus !== 'running') return
    const timer = window.setInterval(() => {
      loadRun()
    }, RUN_POLL_INTERVAL_MS)
    return () => window.clearInterval(timer)
  }, [runStatus, runNotFound, loadRun])

  useEffect(() => {
    if (runStatus !== 'running') return
    const onVisible = () => {
      if (document.visibilityState === 'visible') void loadRun()
    }
    document.addEventListener('visibilitychange', onVisible)
    return () => document.removeEventListener('visibilitychange', onVisible)
  }, [runStatus, loadRun])

  // Acknowledgements take focus (the button was replaced); refusals do not.
  const stopKind = stopOutcome?.kind
  useEffect(() => {
    if (stopKind === 'accepted' || stopKind === 'already_stopping') stopMessageRef.current?.focus()
  }, [stopKind])

  function handleStopOutcome(outcome: StopOutcome) {
    setStopOutcome(outcome)
    void loadRun()
  }

  function applyOutcomeFilter(value: VideoFinalOutcome | '') {
    setPage(1)
    setOutcomeFilter(value)
  }

  if (runNotFound) {
    return (
      <>
        <div className="page-header">
          <h1 className="page-title">Run</h1>
          <button className="button" onClick={() => navigate('/runs')}>
            Back to runs
          </button>
        </div>
        <div className="card empty-state">Run {runId} was not found.</div>
      </>
    )
  }

  if (!run && runContextError) {
    return (
      <>
        <div className="page-header">
          <h1 className="page-title">Run {runId}</h1>
          <button className="button" onClick={() => navigate('/runs')}>Back to runs</button>
        </div>
        <p role="alert">Run context unavailable. Results cannot be interpreted safely.</p>
        <button ref={statusRetryRef} type="button" className="button" onClick={handleRunRetry}>Retry</button>
      </>
    )
  }

  // A failed status read keeps the run state unknown with a visible retry
  // (Task 2.1 criteria); it is not presented as completion or as an empty run.
  if (!run && runReadError) {
    return (
      <>
        <div className="page-header">
          <h1 className="page-title">Run {runId}</h1>
          <button className="button" onClick={() => navigate('/runs')}>
            Back to runs
          </button>
        </div>
        <FetchErrorRegion
          ref={statusRetryRef}
          message="Failed to load run status. The run's current state is unknown; check your connection and try again."
          onRetry={handleRunRetry}
          retrying={runRetrying}
        />
      </>
    )
  }

  if (!run) {
    return (
      <div className="empty-state">
        {admission && <p role="status">{admissionLine(admission, runId)} Checking progress…</p>}
        Loading run {runId} status…
      </div>
    )
  }

  const mode = simulationMode(run.run_type)
  const recordedEmptyDryRunSelection = run.run_type === 'dry_run'
    && run.status === 'completed'
    && run.videos_selected === 0
    && run.videos_evaluated === 0
  const outcomeLabel = mode ? 'Hypothetical outcome' : 'Outcome'
  const isTerminal = run.status === 'failed' || run.status === 'interrupted'
  const timeLimited = stoppedAtTimeLimit(run)
  const stopped = run.status === 'stopped'
  const stopping = isStopping(run)

  return (
    <>
      <div className="page-header">
        <h1 className="page-title" ref={headingRef} tabIndex={-1}>{mode ? `${mode}${mode === 'Dry-run' ? '' : ' run'}` : 'Run'} {run.id}</h1>
        <button className="button" onClick={() => navigate('/runs')}>
          Back to runs
        </button>
      </div>
      {mode && <p role="status">{simulationExplanation}</p>}
      {run.published_range && (
        <p data-testid="published-range">{describePublishedRange(run.published_range)}</p>
      )}

      {/* A refresh failure after a successful load keeps the last stored
          status on screen and states that it may be out of date, with a
          visible retry instead of claiming the refresh succeeded. */}
      {runReadError && (
        <div
          className="card"
          role="alert"
          aria-live="assertive"
          style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 12, marginBottom: 16 }}
        >
          <p style={{ margin: 0, color: 'var(--color-status-failed)' }}>
            Could not refresh status. This run may still be processing. Last known status: {statusText(run)}.
          </p>
          <button type="button" className="button" onClick={handleRunRetry} disabled={runRetrying}>
            {runRetrying ? 'Retrying…' : 'Retry'}
          </button>
        </div>
      )}

      {admission && (
        <div className="card" role="status" aria-live="polite" style={{ marginBottom: 16 }}>
          <p style={{ margin: 0 }}><strong>{admissionLine(admission, run.id)}</strong></p>
          {admission === 'resumed' && (
            <p style={{ margin: '8px 0 0' }}>Playlist additions already recorded as successful are not repeated. An addition whose earlier outcome could not be confirmed stays marked as unconfirmed.</p>
          )}
        </div>
      )}

      {stopOutcome && (
        <StopRunMessage ref={stopMessageRef} outcome={stopOutcome} onRunPage onDismiss={() => setStopOutcome(null)} />
      )}

      {(run.status === 'running' || isTerminal) && (
        <section className="card" role="region" aria-label="Run progress" aria-live="off" style={{ marginBottom: 16 }}>
          <p role="status" aria-live="polite" style={{ margin: 0 }}>{progressAnnouncement(run, runReadError)}</p>
          {run.status === 'running' && !stopping && run.current_phase === 'discovery' && run.videos_selected == null && (
            <p style={{ margin: '8px 0 0' }}>{runReadError ? 'The last recorded phase was discovery. The input count is not available; there is no known completion total. Zero decision/counter values do not mean no videos were found.' : 'Discovery is in progress. The input count is not available yet; there is no known completion total. Zero decision/counter values do not mean no videos were found.'}</p>
          )}
          {run.status === 'running' && run.videos_selected != null && <p style={{ margin: '8px 0 0' }}>Selected inputs: {run.videos_selected}</p>}
          {run.status === 'running' && run.videos_selected == null && run.current_phase !== 'discovery' && <p style={{ margin: '8px 0 0' }}>Selected inputs: Unknown. The selected input count was not captured or is not available.</p>}
          <p style={{ margin: '8px 0 0' }}>Videos with recorded route decisions: {run.videos_evaluated ?? 'Unavailable'}. Counts reflect saved evidence, not overall completion.</p>
          <p style={{ margin: '8px 0 0' }}>Status checked at {statusCheckedAt?.toLocaleTimeString() ?? 'unknown'}.</p>
          {run.status === 'running' && <StopRunControl run={run} onOutcome={handleStopOutcome} />}
        </section>
      )}

      {/* Terminal stored status, including run failure, is visible here on the
          run itself — independent of the Errors queue (Task 2.1 criteria). */}
      {run.status !== 'running' && (
        <div className="card" role="status" aria-live="polite" style={{ marginBottom: 16 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <strong>{stopped ? terminalStatusLine('stopped') : timeLimited ? 'Run interrupted — time limit reached.' : terminalStatusLine(run.status)}</strong>
            {run.completed_at && (
              <span style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                {new Date(run.completed_at).toLocaleString()}
              </span>
            )}
          </div>
          {stopped && (
            <>
              <p style={{ margin: '8px 0 0' }}>
                Last recorded phase: {phaseLabel(run.current_phase) ?? 'unavailable'}.
                {mode && ' Results are incomplete because the run was stopped.'}
              </p>
              {!mode && (
                <p style={{ margin: '8px 0 0' }}>
                  Run {run.id} will not be resumed. Its unfinished videos are in the deferred queue, and the next new run will process them automatically.{' '}
                  <Link to="/errors">View deferred queue</Link>
                </p>
              )}
            </>
          )}
          {isTerminal && (
            <p style={{ margin: '8px 0 0', color: 'var(--color-status-failed)' }}>{failureExplanation(run)}</p>
          )}
          {timeLimited && (
            <p style={{ margin: '8px 0 0' }}>
              Work done up to this point is saved. Pressing Run Now continues the oldest run stopped this way. This run continues only if it is the oldest and can be resumed safely.{' '}
              <Link to="/">Go to Overview</Link>
            </p>
          )}
        </div>
      )}

      {mode && <h2 className="section-title">Hypothetical results</h2>}
      <div className="card-grid">
        <div className="card">
          <div className="metric-value">{selectedCountLabel(run)}</div>
          <div className="metric-label">Selected</div>
        </div>
        <div className="card">
          <div className="metric-value">{run.videos_evaluated ?? '—'}</div>
          <div className="metric-label">{mode ? 'Evaluated videos' : 'Evaluated'}</div>
        </div>
        {!mode && (
          <div className="card">
            <div className="metric-value">{run.videos_discovered}</div>
            <div className="metric-label">Discovered</div>
          </div>
        )}
        <div className="card">
          <div className="metric-value">{run.videos_accepted}</div>
          <div className="metric-label">Accepted</div>
        </div>
        <div className="card">
          <div className="metric-value">{run.videos_rejected}</div>
          <div className="metric-label">Rejected</div>
        </div>
        <div className="card">
          <div className="metric-value">{run.videos_deferred}</div>
          <div className="metric-label">Deferred</div>
        </div>
        <div className="card">
          <div className="metric-value">{run.videos_failed}</div>
          <div className="metric-label">Failed</div>
        </div>
      </div>
      {/* ARD-001: qualify the evaluated count as decision-bearing evidence,
          not proof that every selected video finished evaluation. */}
      <p style={{ margin: '8px 0 16px', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
        Evaluated counts videos with route decisions recorded for this run. Selected shows Unknown when the run's
        input set was never captured; it is not zero.
      </p>

      <div className="card">
        <h2 className="section-title">{mode ? 'Hypothetical video outcomes' : 'Video outcomes'}</h2>
        <div style={{ maxWidth: 240, marginBottom: 16 }}>
          <label className="form-label" htmlFor="run-videos-outcome-filter">
            {outcomeLabel}
          </label>
          <select
            id="run-videos-outcome-filter"
            className="form-select"
            value={outcomeFilter}
            onChange={(e) => applyOutcomeFilter(e.target.value as VideoFinalOutcome | '')}
          >
            <option value="">All outcomes</option>
            {outcomeOptions.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </div>
        {videosLoading ? (
          <div className="empty-state" role="status">Loading {mode ? 'hypothetical video outcomes' : 'video outcomes'}…</div>
        ) : videosError ? (
          <div role="alert" style={{ color: 'var(--color-status-failed)' }}>
            <p style={{ margin: '0 0 8px' }}>
              Failed to load {mode ? 'hypothetical video outcomes' : 'video outcomes'}. Check your connection and try
              again.
            </p>
            <button type="button" className="button" onClick={() => loadVideos()}>
              Reload videos
            </button>
          </div>
        ) : videos.length === 0 ? (
          outcomeFilter ? (
            <div className="empty-state">
              <p style={{ margin: 0 }}>No videos match the current filters.</p>
              <button type="button" className="button" onClick={() => applyOutcomeFilter('')}>
                Clear outcome filter
              </button>
            </div>
          ) : (
            <div className="empty-state">
              {mode ? (
                recordedEmptyDryRunSelection && !stopped ? (
                  <>
                    <p>No videos were selected for this dry run.</p>
                    <p>Dry runs evaluate stored videos awaiting their first evaluation. They do not discover new videos or re-evaluate previously processed videos.</p>
                  </>
                ) : stopped ? (
                  'No results were recorded before the run was stopped.'
                ) : run.status === 'running' ? (
                  'No hypothetical video outcomes recorded yet.'
                ) : (
                  'No hypothetical video outcomes were recorded for this run.'
                )
              ) : 'No video outcomes yet.'}
            </div>
          )
        ) : (
          <>
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
              Video information is current/local display context, not necessarily the information evaluated by this run.
            </p>
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Title</th>
                  <th scope="col">Source channel</th>
                  <th scope="col">{outcomeLabel}</th>
                </tr>
              </thead>
              <tbody>
                {videos.map((v) => {
                  const observedAt = currentLocalObservedAt(v.current_local_metadata?.source_fetched_at)
                  return <tr key={v.id}>
                    <td data-label="Title">
                      <Link to={`/runs/${runId}/video/${v.video_id}`} aria-label={`${currentMetadata(v.current_local_metadata, 'title')}. Video ID: ${v.video_id}`}>
                        {currentMetadata(v.current_local_metadata, 'title')}
                        {(v.current_local_metadata?.title_availability === 'current_local' || v.current_local_metadata?.channel_name_availability === 'current_local') && <small style={{ display: 'block', marginTop: 4 }}>Current/local information</small>}
                        <small style={{ display: 'block', marginTop: 4 }}>Video ID: {v.video_id}</small>
                        {(v.current_local_metadata?.title_availability === 'current_local' || v.current_local_metadata?.channel_name_availability === 'current_local') && observedAt && <small style={{ display: 'block', marginTop: 4 }}>{observedAt}</small>}
                      </Link>
                    </td>
                    <td data-label="Source channel">
                      <span style={{ display: 'block' }}>{currentMetadata(v.current_local_metadata, 'channel_name')}</span>
                      <small style={{ display: 'block', marginTop: 4 }}>Channel ID: {v.channel_id}</small>
                    </td>
                    <td data-label={outcomeLabel}>
                       <Badge state={v.processing_state} finalOutcome={v.final_outcome} mode={run.run_type} />
                       {v.route_decisions?.map((d) => d.decision === 'skipped' && d.skip_reason === 'exclusion'
                         ? <CausalExclusion key={d.route_id} decision={d} showRoute />
                         : <div key={d.route_id} style={{ overflowWrap: 'anywhere' }}><p>{routeAttribution(d)}</p><p>{deriveRouteDecision(d.decision, d.skip_reason).label}</p></div>)}
                      {v.unresolved_mutations?.length ? <p>Playlist outcome unknown</p> : null}
                      {v.final_outcome === 'deferred' && v.deferred_reason === 'authorization_pending' && <p>Waiting for YouTube access</p>}
                    </td>
                  </tr>
                })}
              </tbody>
            </table>
            {videos.some((v) => v.metadata_context_status) && (
              <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                {videos.some((v) => v.metadata_context_status === 'context_read_failed') && (
                  <> Could not load local video information for some videos. <button type="button" className="button" onClick={() => loadVideos()}>Retry local information</button></>
                )}
              </p>
            )}
            {videoPageCount > 1 && (
              <nav
                aria-label="Run video pages"
                style={{ display: 'flex', alignItems: 'center', gap: 12, marginTop: 12 }}
              >
                <button
                  type="button"
                  className="button"
                  onClick={() => setPage((p) => Math.max(1, p - 1))}
                  disabled={page <= 1}
                >
                  Previous page
                </button>
                <span aria-live="polite" style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                  Page {page} of {videoPageCount}
                </span>
                <button
                  type="button"
                  className="button"
                  onClick={() => setPage((p) => p + 1)}
                  disabled={page >= videoPageCount}
                >
                  Next page
                </button>
              </nav>
            )}
            {videoTotal > videos.length && (
              <p style={{ margin: '8px 0 0', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                {videoPageCount > 1
                  ? `Showing ${(page - 1) * VIDEO_PAGE_SIZE + 1}–${(page - 1) * VIDEO_PAGE_SIZE + videos.length} of ${videoTotal} videos.`
                  : `Showing ${videos.length} of ${videoTotal} videos.`}
              </p>
            )}
          </>
        )}
      </div>
    </>
  )
}
