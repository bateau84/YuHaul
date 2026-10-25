import { useEffect, useRef, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { Check, X, AlertTriangle, Ban, Clock, Minus, PlayCircle } from 'lucide-react'
import * as api from '../api/client'
import { Badge } from '../components/Badge'
import { deriveRouteDecision, deriveActionOutcome, formatDuration, simulationExplanation, simulationMode } from '../lib/outcome'
import { shortRouteId } from '../lib/route'
import type { Run, DecisionDetail, RouteDecision, ConditionAudit, ActionOutcome } from '../types'

const actionIcons = {
  accepted: Check,
  rejected: X,
  excluded: Ban,
  deferred: Clock,
  failed: AlertTriangle,
  'not-evaluated': Minus,
  'in-progress': PlayCircle,
}

function ActionOutcomeBadge({ outcome }: { outcome: string }) {
  const { variant, label } = deriveActionOutcome(outcome)
  const Icon = actionIcons[variant]
  return (
    <span className={`badge badge--${variant}`}>
      <Icon size={14} aria-hidden="true" />
      {label}
    </span>
  )
}

function DecisionBadge({ decision, skipReason }: { decision: RouteDecision['decision']; skipReason?: RouteDecision['skip_reason'] }) {
  const { variant, label, reason } = deriveRouteDecision(decision, skipReason)
  const icons = {
    accepted: Check,
    rejected: X,
    excluded: Ban,
    deferred: Clock,
    failed: AlertTriangle,
    'not-evaluated': Minus,
    'in-progress': PlayCircle,
  }
  const Icon = icons[variant]
  return (
    <div>
      <span className={`badge badge--${variant}`}>
        <Icon size={14} aria-hidden="true" />
        {label}
      </span>
      {reason && <p style={{ margin: '4px 0 0', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>{reason}</p>}
    </div>
  )
}

function ConditionAuditRow({ audit }: { audit: ConditionAudit }) {
  return (
    <li style={{ display: 'flex', alignItems: 'flex-start', gap: 8, padding: '6px 0' }}>
      {audit.matched ? (
        <Check size={18} color="var(--color-status-accepted)" aria-hidden="true" />
      ) : (
        <X size={18} color="var(--color-status-rejected)" aria-hidden="true" />
      )}
      <span>
        <strong>{audit.field}</strong> {audit.operator} <code>{audit.expected_value}</code>
        {audit.actual_value !== undefined && (
          <span style={{ color: 'var(--color-text-muted)' }}> — actual: {audit.actual_value}</span>
        )}
        <span className="sr-only">{audit.matched ? ' matched' : ' did not match'}</span>
      </span>
    </li>
  )
}

function ActionRow({ action }: { action: ActionOutcome }) {
  const { variant, label } = deriveActionOutcome(action.outcome)
  const icons = {
    accepted: Check,
    rejected: X,
    excluded: Ban,
    deferred: Clock,
    failed: AlertTriangle,
    'not-evaluated': Minus,
    'in-progress': PlayCircle,
  }
  const Icon = icons[variant]
  return (
    <li style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 0' }}>
      <span className={`badge badge--${variant}`}>
        <Icon size={14} aria-hidden="true" />
        {label}
      </span>
      <span>
        {action.action_type} → {action.target_playlist_id}
      </span>
      {action.error_message && (
        <span style={{ color: 'var(--color-status-rejected)', fontSize: '0.875rem' }}>{action.error_message}</span>
      )}
    </li>
  )
}

function DecisionRow({ decision, index, simulation }: { decision: RouteDecision; index: number; simulation: boolean }) {
  return (
    <div className="card" style={{ marginBottom: 12 }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-start',
          gap: 12,
          flexWrap: 'wrap',
        }}
      >
        <div>
          <span
            style={{
              display: 'inline-block',
              width: 24,
              height: 24,
              borderRadius: '50%',
              background: 'var(--color-surface-elevated)',
              color: 'var(--color-text-secondary)',
              textAlign: 'center',
              lineHeight: '24px',
              fontSize: '0.75rem',
              fontWeight: 700,
              marginRight: 8,
            }}
            aria-hidden="true"
          >
            {index + 1}
          </span>
          <strong>
            {decision.route_name}{' '}
            <span className="route-short-id">({shortRouteId(decision.route_id)})</span>
          </strong>
        </div>
        <DecisionBadge decision={decision.decision} skipReason={decision.skip_reason} />
      </div>
      {decision.rejection_reason && (
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', marginTop: 8 }}>
          Rejection reason: {decision.rejection_reason}
        </p>
      )}
      {(decision.condition_audits ?? []).length > 0 && (
        <div style={{ marginTop: 12 }}>
          <strong style={{ fontSize: '0.875rem', color: 'var(--color-text-secondary)' }}>Conditions evaluated</strong>
          <ul style={{ listStyle: 'none', padding: 0, margin: '8px 0 0' }}>
            {(decision.condition_audits ?? []).map((c) => (
              <ConditionAuditRow key={c.id} audit={c} />
            ))}
          </ul>
        </div>
      )}
      {(decision.actions ?? []).length > 0 && (
        <div style={{ marginTop: 12 }}>
          <strong style={{ fontSize: '0.875rem', color: 'var(--color-text-secondary)' }}>{simulation ? 'Simulated actions' : 'Actions'}</strong>
          <ul style={{ listStyle: 'none', padding: 0, margin: '8px 0 0' }}>
            {(decision.actions ?? []).map((a) => (
              <ActionRow key={a.id} action={a} />
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

export function DecisionDetailView() {
  const { id, videoId } = useParams<{ id: string; videoId: string }>()
  // A new location must not inherit interpreted results from the previous run.
  return <DecisionDetailContent key={`${id}/${videoId}`} id={id} videoId={videoId} />
}

function DecisionDetailContent({ id, videoId }: { id?: string; videoId?: string }) {
  const [detail, setDetail] = useState<DecisionDetail | null>(null)
  const [run, setRun] = useState<Run | null>(null)
  const [contextError, setContextError] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const loaded = run !== null && detail !== null
  useEffect(() => {
    if (loaded && attempt === 0) headingRef.current?.focus()
  }, [loaded, attempt])

  useEffect(() => {
    let active = true
    api.getRun(Number(id)).then((result) => {
      if (!active) return
      if (!result || !['manual', 'scheduled', 'dry_run', 'replay', 'backtest'].includes(result.run_type)) {
        setContextError(true)
        return
      }
      setRun(result)
    }).catch(() => { if (active) setContextError(true) })
    return () => { active = false }
  }, [id, attempt])

  useEffect(() => {
    if (!id || !videoId) return
    api.getDecisionDetail(Number(id), videoId).then((d) => setDetail(d ?? null))
  }, [id, videoId])

  if (!run) return (
    <>
      <div className="page-header">
        <h1 className="page-title" tabIndex={-1}>Decision Detail</h1>
        <Link to={`/runs/${id}`} className="button">Back to run</Link>
      </div>
      <p>Run {id}</p>
      {contextError ? <>
        <p role="alert">Run context unavailable. Results cannot be interpreted safely.</p>
        <button className="button" onClick={() => { setContextError(false); setAttempt((n) => n + 1) }}>Retry</button>
      </> : <p role="status">Loading run context…</p>}
    </>
  )
  if (!detail) return <div className="empty-state">Loading decision detail...</div>

  const video = detail.video
  const mode = simulationMode(run.run_type)

  return (
    <>
      <div className="page-header">
        <h1 className="page-title" ref={headingRef} tabIndex={-1}>{mode ? `${mode} decision detail` : 'Decision Detail'}</h1>
        <Link to={`/runs/${id}`} className="button">
          Back to run
        </Link>
      </div>
      {mode && <><p>Run {id}</p><p role="status">{simulationExplanation}</p></>}

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">{video.title ?? video.video_id}</h2>
        <p style={{ color: 'var(--color-text-muted)' }}>{video.channel_name ?? video.channel_id}</p>
        <dl className="metadata-list" style={{ marginTop: 12 }}>
          <div>
            <dt>Duration</dt>
            <dd>{formatDuration(video.duration_seconds)}</dd>
          </div>
          {video.published_at && (
            <div>
              <dt>Published</dt>
              <dd>{new Date(video.published_at).toLocaleDateString()}</dd>
            </div>
          )}
          <div>
            <dt>Video ID</dt>
            <dd>{video.video_id}</dd>
          </div>
        </dl>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">{mode ? 'Layer 1 — Hypothetical outcome' : 'Layer 1 — Processing outcome'}</h2>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <Badge state={detail.processing_state} finalOutcome={detail.final_outcome} mode={run.run_type} />
          {!mode && <span style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
            Processing state: <code>{detail.processing_state}</code>
          </span>}
        </div>
        {!mode && video.deferred_reason && (
          <p style={{ marginTop: 8, color: 'var(--color-text-muted)' }}>Deferred reason: {video.deferred_reason}</p>
        )}
        {!mode && video.error_message && (
          <p style={{ marginTop: 8, color: 'var(--color-status-rejected)' }}>{video.error_message}</p>
        )}
      </div>

      <h2 className="section-title">Layer 2 — Route decisions</h2>
      <p style={{ color: 'var(--color-text-muted)', marginBottom: 12 }}>
        Routes evaluated in order. Numbers show evaluation sequence.
      </p>
      {(detail.route_decisions ?? []).map((d, i) => (
        <DecisionRow key={d.id} decision={d} index={i} simulation={!!mode} />
      ))}

      <div className="card" style={{ marginTop: 16 }}>
        <h2 className="section-title">{mode ? 'Layer 3 — Simulated action outcomes' : 'Layer 3 — Action outcomes'}</h2>
        {mode && <p>These actions were not executed. Accepted means the video matched this simulation's routing rules, not that a playlist was changed.</p>}
      {(detail.route_decisions ?? []).some((d) => (d.actions ?? []).length > 0) ? (
        <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
          {(detail.route_decisions ?? []).flatMap((d) =>
            (d.actions ?? []).map((a) => (
              <li key={`${d.id}-${a.id}`} style={{ marginBottom: 8 }}>
                <strong>
                  {d.route_name}{' '}
                  <span className="route-short-id">({shortRouteId(d.route_id)})</span>
                </strong>:
                <span style={{ marginLeft: 8 }}>
                  <ActionOutcomeBadge outcome={a.outcome} />
                </span>
                <span style={{ marginLeft: 8 }}>
                  {a.action_type} → {a.target_playlist_id}
                </span>
                {a.error_message && (
                  <span style={{ color: 'var(--color-status-rejected)', fontSize: '0.875rem', marginLeft: 8 }}>
                    {a.error_message}
                  </span>
                )}
              </li>
            ))
          )}
        </ul>
      ) : (
        <p style={{ color: 'var(--color-text-muted)' }}>{mode ? 'No simulated actions recorded for this video.' : 'No action outcomes recorded for this video.'}</p>
      )}
      </div>
    </>
  )
}
