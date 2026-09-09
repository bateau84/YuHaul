import { useEffect, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { Check, X } from 'lucide-react'
import * as api from '../api/client'
import { Badge } from '../components/Badge'
import type { DecisionDetail, RouteDecision } from '../types'

function DecisionRow({ decision }: { decision: RouteDecision }) {
  return (
    <div className="card" style={{ marginBottom: 12 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <strong>{decision.route_name}</strong>
        <span className="badge badge--not-evaluated">{decision.decision}</span>
      </div>
      {decision.skip_reason && <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>Skip reason: {decision.skip_reason}</p>}
      {decision.condition_audits.length > 0 && (
        <ul style={{ listStyle: 'none', padding: 0, margin: '12px 0 0' }}>
          {decision.condition_audits.map((c) => (
            <li key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 0' }}>
              {c.matched ? (
                <Check size={16} color="var(--color-status-accepted)" aria-hidden="true" />
              ) : (
                <X size={16} color="var(--color-status-rejected)" aria-hidden="true" />
              )}
              <span>
                {c.field} {c.operator} {c.expected_value}
                {c.actual_value !== undefined && ` (actual: ${c.actual_value})`}
              </span>
            </li>
          ))}
        </ul>
      )}
      {decision.actions.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <strong>Actions:</strong>
          <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
            {decision.actions.map((a) => (
              <li key={a.id} style={{ fontSize: '0.875rem' }}>
                {a.action_type} → {a.target_playlist_id}: {a.outcome}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

export function DecisionDetailView() {
  const { id, videoId } = useParams<{ id: string; videoId: string }>()
  const [detail, setDetail] = useState<DecisionDetail | null>(null)

  useEffect(() => {
    api.getDecisionDetail(Number(id), Number(videoId)).then((d) => setDetail(d ?? null))
  }, [id, videoId])

  if (!detail) return <div className="empty-state">Loading decision detail...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Decision Detail</h1>
        <Link to={`/runs/${id}`} className="button">
          Back to run
        </Link>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">{detail.video.title ?? detail.video.video_id}</h2>
        <p style={{ color: 'var(--color-text-muted)' }}>{detail.video.channel_name ?? detail.video.channel_id}</p>
        <div style={{ marginTop: 12 }}>
          <Badge state={detail.processing_state} finalOutcome={detail.final_outcome} />
        </div>
      </div>

      <h2 className="section-title">Evidence trail</h2>
      {detail.route_decisions.map((d) => (
        <DecisionRow key={d.id} decision={d} />
      ))}
    </>
  )
}
