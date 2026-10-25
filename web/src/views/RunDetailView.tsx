import { useCallback, useEffect, useRef, useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import * as api from '../api/client'
import { Badge } from '../components/Badge'
import { useToast } from '../hooks/useToast'
import { simulationExplanation, simulationMode, videoOutcomeLabels } from '../lib/outcome'
import type { Run, VideoFinalOutcome, VideoOutcome } from '../types'

// The run videos endpoint caps page_size at 100 (internal/server/run_handlers.go).
// Request the maximum so the outcome filter and the table cover as much of the
// run as a single page can hold; the server-side total is shown whenever more
// videos remain beyond the loaded page.
const VIDEO_PAGE_SIZE = 100

const outcomeOptions = Object.entries(videoOutcomeLabels) as [VideoFinalOutcome, string][]

export function RunDetailView() {
  const { id } = useParams<{ id: string }>()
  return <RunDetailContent key={id} id={id} />
}

function RunDetailContent({ id }: { id?: string }) {
  const navigate = useNavigate()
  const { addToast } = useToast()
  const runId = Number(id)
  const [run, setRun] = useState<Run | null>(null)
  const [videos, setVideos] = useState<VideoOutcome[]>([])
  const [videoTotal, setVideoTotal] = useState(0)
  const [videosError, setVideosError] = useState(false)
  const [outcomeFilter, setOutcomeFilter] = useState<VideoFinalOutcome | ''>('')
  const [phase, setPhase] = useState<string | undefined>()
  const [connected, setConnected] = useState(false)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const loaded = run !== null
  useEffect(() => {
    if (loaded) headingRef.current?.focus()
  }, [loaded])

  useEffect(() => {
    api.getRun(runId).then((r) => setRun(r ?? null))
  }, [runId])

  // BR-032 AC5: the outcome filter is applied server-side, so the empty state
  // and the shown total reflect the whole run for the selected category, not
  // just a pre-filtered page. Only the latest request may update the list, so
  // a slow response for an earlier filter selection cannot overwrite it.
  const videosRequestRef = useRef(0)
  const loadVideos = useCallback(() => {
    const requestId = ++videosRequestRef.current
    return api
      .getRunVideos(runId, { outcome: outcomeFilter || undefined, pageSize: VIDEO_PAGE_SIZE })
      .then((page) => {
        if (requestId !== videosRequestRef.current) return
        setVideos(page.videos)
        setVideoTotal(page.total)
        setVideosError(false)
      })
      .catch(() => {
        if (requestId !== videosRequestRef.current) return
        setVideos([])
        setVideoTotal(0)
        setVideosError(true)
      })
  }, [runId, outcomeFilter])

  useEffect(() => {
    loadVideos()
  }, [loadVideos])

  // Refresh the videos through the current filter without tearing down the
  // run-completion subscription on every filter change.
  const loadVideosRef = useRef(loadVideos)
  useEffect(() => {
    loadVideosRef.current = loadVideos
  }, [loadVideos])

  useEffect(() => {
    if (!run || run.status !== 'running') return
    const unsubscribe = api.subscribeRunSSE(runId, {
      onOpen: () => setConnected(true),
      onEvent: (ev) => {
        if (ev.type === 'run.phase') {
          const data = ev.data as { phase?: string }
          setPhase(data.phase)
        }
        if (ev.type === 'run.completed') {
          setPhase(undefined)
          api.getRun(runId).then((r) => setRun(r ?? null))
          loadVideosRef.current()
          addToast('Run completed', 'success')
        }
      },
      onError: () => {
        setConnected(false)
        addToast('Run progress connection lost', 'error')
      },
    })
    return unsubscribe
  }, [run, runId, addToast])

  if (!run) return <div className="empty-state">Loading run...</div>
  const mode = simulationMode(run.run_type)
  const outcomeLabel = mode ? 'Hypothetical outcome' : 'Outcome'

  return (
    <>
      <div className="page-header">
        <h1 className="page-title" ref={headingRef} tabIndex={-1}>{mode ? `${mode}${mode === 'Dry-run' ? '' : ' run'}` : 'Run'} {run.id}</h1>
        <button className="button" onClick={() => navigate('/runs')}>
          Back to runs
        </button>
      </div>
      {mode && <p role="status">{simulationExplanation}</p>}

      {run.status === 'running' && (
        <div className="card" role="status" aria-live="polite" style={{ marginBottom: 16 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <span
              style={{
                width: 16,
                height: 16,
                borderRadius: '50%',
                background: connected ? 'var(--color-status-accepted)' : 'var(--color-status-deferred)',
              }}
              aria-hidden="true"
            />
            <span>
              {phase ? `Processing: ${phase.replace(/_/g, ' ')}` : 'Run in progress'}
            </span>
          </div>
        </div>
      )}

      {mode && <h2 className="section-title">Hypothetical results</h2>}
      <div className="card-grid">
        <div className="card">
          <div className="metric-value">{run.videos_discovered}</div>
          <div className="metric-label">{mode ? 'Evaluated videos' : 'Discovered'}</div>
        </div>
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
            onChange={(e) => setOutcomeFilter(e.target.value as VideoFinalOutcome | '')}
          >
            <option value="">All outcomes</option>
            {outcomeOptions.map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </div>
        {videosError ? (
          <p role="alert" style={{ color: 'var(--color-status-failed)', margin: 0 }}>
            Failed to load {mode ? 'hypothetical video outcomes' : 'video outcomes'}. Check your connection and try again.
          </p>
        ) : videos.length === 0 ? (
          outcomeFilter ? (
            <div className="empty-state">
              <p style={{ margin: 0 }}>No videos match the current filters.</p>
              <button type="button" className="button" onClick={() => setOutcomeFilter('')}>
                Clear outcome filter
              </button>
            </div>
          ) : (
            <div className="empty-state">{mode ? 'No hypothetical video outcomes yet.' : 'No video outcomes yet.'}</div>
          )
        ) : (
          <>
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Title</th>
                  <th scope="col">Channel</th>
                  <th scope="col">{outcomeLabel}</th>
                </tr>
              </thead>
              <tbody>
                {videos.map((v) => (
                  <tr key={v.id}>
                    <td data-label="Title">
                      <Link to={`/runs/${runId}/video/${v.video_id}`}>{v.title ?? v.video_id}</Link>
                    </td>
                    <td data-label="Channel">{v.channel_name ?? v.channel_id}</td>
                    <td data-label={outcomeLabel}>
                      <Badge state={v.processing_state} finalOutcome={v.final_outcome} mode={run.run_type} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            {videoTotal > videos.length && (
              <p style={{ margin: '8px 0 0', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                Showing {videos.length} of {videoTotal} videos.
              </p>
            )}
          </>
        )}
      </div>
    </>
  )
}
