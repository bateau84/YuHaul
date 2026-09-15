import { useEffect, useState } from 'react'
import { useParams, Link, useNavigate } from 'react-router-dom'
import * as api from '../api/client'
import { Badge } from '../components/Badge'
import { useToast } from '../hooks/useToast'
import type { Run, VideoOutcome } from '../types'

export function RunDetailView() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { addToast } = useToast()
  const runId = Number(id)
  const [run, setRun] = useState<Run | null>(null)
  const [videos, setVideos] = useState<VideoOutcome[]>([])
  const [phase, setPhase] = useState<string | undefined>()
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    api.getRun(runId).then((r) => setRun(r ?? null))
    api.getRunVideos(runId).then(setVideos)
  }, [runId])

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
          api.getRunVideos(runId).then(setVideos)
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

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Run {run.id}</h1>
        <button className="button" onClick={() => navigate('/runs')}>
          Back to runs
        </button>
      </div>

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

      <div className="card-grid">
        <div className="card">
          <div className="metric-value">{run.videos_discovered}</div>
          <div className="metric-label">Discovered</div>
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
        <h2 className="section-title">Video outcomes</h2>
        {videos.length === 0 ? (
          <div className="empty-state">No video outcomes yet.</div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Title</th>
                <th scope="col">Channel</th>
                <th scope="col">Outcome</th>
              </tr>
            </thead>
            <tbody>
              {videos.map((v) => (
                <tr key={v.id}>
                  <td data-label="Title">
                    <Link to={`/runs/${runId}/video/${v.video_id}`}>{v.title ?? v.video_id}</Link>
                  </td>
                  <td data-label="Channel">{v.channel_name ?? v.channel_id}</td>
                  <td data-label="Outcome">
                    <Badge state={v.processing_state} finalOutcome={v.final_outcome} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  )
}
