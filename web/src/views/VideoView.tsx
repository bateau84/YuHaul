import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useLocation, useParams } from 'react-router-dom'
import { getVideo } from '../api/client'
import { ApiError } from '../api/core'
import { OutcomeBadge, RunLink } from '../components/VideoParts'
import { channelLabel, echo, formatWhen, mutedStyle, reasonLabel, runLabel, titleUnavailableLabel } from '../lib/video-copy'
import type { RunType, VideoLatestOutcome, VideoQueueState, VideoRunEntry, VideoView as VideoViewData } from '../types'

const VIDEO_ID = /^[A-Za-z0-9_-]{11}$/
const RUNS_SHOWN = 10

const modeLabels: Record<RunType, string> = {
  manual: 'Run Now',
  scheduled: 'Scheduled',
  dry_run: 'Dry run (simulation)',
  replay: 'Replay (simulation)',
  backtest: 'Backtest (simulation)',
}

type Load =
  | { phase: 'loading' }
  | { phase: 'ready'; video: VideoViewData }
  | { phase: 'unknown' }
  | { phase: 'failed' }

function LatestOutcome({ outcome }: { outcome: VideoLatestOutcome }) {
  switch (outcome.state) {
    case 'committed': {
      const when = formatWhen(outcome.committed_at)
      return (
        <p>
          <OutcomeBadge outcome={outcome.outcome} />{' '}
          {outcome.run_id !== null ? (
            <>
              Committed in <RunLink id={outcome.run_id} />
            </>
          ) : (
            <>Committed</>
          )}
          {when ? ` on ${when}.` : '.'}
        </p>
      )
    }
    case 'imported_reference':
      return <p>Imported duplicate reference. YuHaul has never processed this video.</p>
    case 'simulation_only':
      return <p>Simulation only. No production run has decided this video, so there is no production outcome.</p>
    default:
      return <p>Not yet evaluated. No production run has committed an outcome for this video.</p>
  }
}

function QueueState({ state }: { state: VideoQueueState }) {
  switch (state.state) {
    case 'deferred_pending':
      return (
        <p>
          In the deferred queue — pending. Reason: {reasonLabel(state.reason)}. <Link to="/errors">View deferred queue</Link>
        </p>
      )
    case 'deferred_claimed':
      return (
        <p>
          In the deferred queue — claimed by run {state.claiming_run_id ?? 'unknown'}. Reason: {reasonLabel(state.reason)}.{' '}
          {state.claiming_run_id !== null && <Link to={`/runs/${state.claiming_run_id}`}>View run {state.claiming_run_id}</Link>}
        </p>
      )
    case 'errors_active':
      return (
        <p>
          In the Errors queue — active. <Link to="/errors">View Errors</Link>
        </p>
      )
    case 'errors_claimed':
      return (
        <p>
          In the Errors queue — claimed by run {state.claiming_run_id ?? 'unknown'}.{' '}
          {state.claiming_run_id !== null && <Link to={`/runs/${state.claiming_run_id}`}>View run {state.claiming_run_id}</Link>}
        </p>
      )
    case 'removed_by_user':
      return <p>Removed from a queue by you. It will not be retried.</p>
    default:
      return <p>Not in any queue.</p>
  }
}

function RunItem({ run }: { run: VideoRunEntry }) {
  const simulation = run.mode === 'simulation'
  return (
    <li className="card" style={{ marginBottom: 8, display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
      <span>
        <RunLink id={run.run_id} /> · {modeLabels[run.run_type] ?? run.run_type} ·{' '}
        <OutcomeBadge outcome={run.outcome} mode={simulation ? run.run_type : undefined} />
      </span>
      <Link to={run.decision_detail_path} style={{ display: 'inline-block', minHeight: 24 }}>
        Open decision detail for {runLabel(run.run_id).toLowerCase()}
      </Link>
    </li>
  )
}

export function VideoView() {
  const { videoId = '' } = useParams()
  const location = useLocation()
  const backTo = (location.state as { from?: string } | null)?.from ?? '/videos'
  const [load, setLoad] = useState<Load>({ phase: 'loading' })
  const [nonce, setNonce] = useState(0)
  const [showAll, setShowAll] = useState(false)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const valid = VIDEO_ID.test(videoId)

  useEffect(() => {
    document.title = 'Videos — YuHaul'
  }, [])

  useEffect(() => {
    if (!valid) return
    let cancelled = false
    setLoad({ phase: 'loading' })
    setShowAll(false)
    getVideo(videoId)
      .then((video) => {
        if (!cancelled) setLoad({ phase: 'ready', video })
      })
      .catch((err: unknown) => {
        if (cancelled) return
        setLoad(err instanceof ApiError && err.status === 404 ? { phase: 'unknown' } : { phase: 'failed' })
      })
    return () => {
      cancelled = true
    }
  }, [videoId, valid, nonce])

  // After opening a video, focus goes to its H1 (SC 2.4.3).
  useEffect(() => {
    if (load.phase === 'ready') headingRef.current?.focus()
  }, [load])

  const retry = useCallback(() => setNonce((n) => n + 1), [])
  const back = (
    <p>
      <Link to={backTo}>Back to search</Link>
    </p>
  )

  if (!valid) {
    return (
      <div>
        {back}
        <h1 className="page-title" tabIndex={-1}>
          Video
        </h1>
        <p role="alert" className="form-error">
          “{echo(videoId)}” is not a valid video ID. A video ID has 11 characters: letters, digits, - or _.
        </p>
      </div>
    )
  }
  if (load.phase === 'loading') {
    return (
      <div>
        {back}
        <h1 className="page-title" tabIndex={-1}>
          Video {videoId}
        </h1>
        <p role="status">Loading…</p>
      </div>
    )
  }
  if (load.phase === 'failed') {
    return (
      <div>
        {back}
        <h1 className="page-title" tabIndex={-1}>
          Video {videoId}
        </h1>
        <div className="card" role="alert" style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 12 }}>
          <p style={{ margin: 0 }}>Could not load this video. Nothing was changed.</p>
          <button type="button" className="button" onClick={retry}>
            Retry
          </button>
        </div>
      </div>
    )
  }
  if (load.phase === 'unknown') {
    return (
      <div>
        <h1 className="page-title" tabIndex={-1}>
          Video {videoId}
        </h1>
        <p role="status">YuHaul knows no video with the ID {videoId}.</p>
        {back}
      </div>
    )
  }

  const { video } = load
  const title = video.title.text
  const runs = video.runs
  const visible = showAll ? runs : runs.slice(0, RUNS_SHOWN)
  const hidden = runs.length - visible.length
  const hasSimulation = runs.some((r) => r.mode === 'simulation')

  return (
    <div>
      {back}
      <h1 className="page-title" tabIndex={-1} ref={headingRef}>
        {title ?? `Video ${video.video_id}`}
      </h1>
      <p style={mutedStyle}>Video ID: {video.video_id}</p>
      <p style={mutedStyle}>Shown from YuHaul's own records. YouTube was not asked.</p>

      <section className="card" style={{ marginBottom: 16 }}>
        {title ? (
          <p style={{ margin: 0 }}>Current/local information — not necessarily the information evaluated by any run.</p>
        ) : (
          <p style={{ margin: 0 }}>{titleUnavailableLabel(video.title.availability)}</p>
        )}
        <p style={{ margin: '8px 0 0' }}>
          {channelLabel(video.channel)}
          {video.channel.name && <span style={mutedStyle}> Current/local information</span>}
        </p>
      </section>

      <section aria-labelledby="video-latest">
        <h2 id="video-latest" className="section-title">
          Latest production outcome
        </h2>
        <LatestOutcome outcome={video.latest_outcome} />
      </section>

      <section aria-labelledby="video-queue">
        <h2 id="video-queue" className="section-title">
          Queue state
        </h2>
        <QueueState state={video.queue_state} />
      </section>

      <section aria-labelledby="video-runs">
        <h2 id="video-runs" className="section-title">
          Runs that decided this video
        </h2>
        {runs.length === 0 ? (
          <p>No run has recorded a decision for this video.</p>
        ) : (
          <>
            {hasSimulation && <p>Simulation runs are what-if results. They never change the latest outcome or the queue state.</p>}
            <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
              {visible.map((r) => (
                <RunItem key={r.run_id} run={r} />
              ))}
            </ul>
            {hidden > 0 && (
              <button type="button" className="button" style={{ minHeight: 44 }} onClick={() => setShowAll(true)}>
                Show {hidden} more runs
              </button>
            )}
          </>
        )}
      </section>
    </div>
  )
}
