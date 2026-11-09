import { useEffect, useRef, useState } from 'react'
import { useParams, Link } from 'react-router-dom'
import { Check, X, AlertTriangle, Ban, Clock, Minus, PlayCircle } from 'lucide-react'
import * as api from '../api/client'
import { Badge } from '../components/Badge'
import { CausalExclusion } from '../components/CausalExclusion'
import { routeAttribution } from '../lib/route-attribution'
import { deriveRouteDecision, actionOutcomeLabel, actionOutcomeReasonLabel, formatDuration, simulationExplanation, simulationMode, type BadgeVariant } from '../lib/outcome'
import { shortRouteId } from '../lib/route'
import { currentLocalObservedAt, currentLocalUnavailableLabel } from '../lib/current-local-metadata'
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

function ActionOutcomeBadge({ outcome, reason, simulation }: { outcome: string; reason?: string; simulation?: boolean }) {
  const { variant, label } = actionBadge(outcome, reason, simulation)
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
      {reason && skipReason !== 'exclusion' && <p style={{ margin: '4px 0 0', color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>{reason}</p>}
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

// A simulation never attempts a YouTube action (ARD-001 / INV-8): its action
// badge is always the neutral "Not attempted", never Succeeded or Executed,
// regardless of any legacy stored outcome string.
function actionBadge(outcome: string, reason?: string, simulation?: boolean): { variant: BadgeVariant; label: string } {
  if (simulation) return { variant: 'not-evaluated', label: 'Not attempted' }
  return actionOutcomeLabel(outcome, reason)
}

function attemptedLabel(iso: string): string {
  const attempt = new Date(iso)
  return `Attempted ${Number.isNaN(attempt.getTime()) ? iso : attempt.toLocaleString()}`
}

function currentField(value: string | number | null | undefined, availability: string | undefined, fallback: string): string {
  if (availability === 'current_local' && value != null) return String(value)
  return fallback
}

// Durable requested-run evidence for one action row (ARD-001): attempt
// timestamp only when a YouTube action was actually attempted, the safe
// outcome reason when one was recorded, and either the vetted error message
// or an explicit unavailable state — never an inferred result. Simulation
// rows render only their reason: no attempt timestamp or provider error may
// appear as execution evidence for a hypothetical action.
function ActionOutcomeEvidence({ action, simulation }: { action: ActionOutcome; simulation: boolean }) {
  const detail = { color: 'var(--color-text-muted)', fontSize: '0.875rem' }
  if (simulation) {
    return action.outcome_reason ? <span style={detail}>{actionOutcomeReasonLabel(action.outcome_reason)}</span> : null
  }
  return (
    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      {action.executed_at && <span style={detail}>{attemptedLabel(action.executed_at)}</span>}
      {action.outcome_reason && <span style={detail}>{actionOutcomeReasonLabel(action.outcome_reason)}</span>}
      {action.outcome_reason === 'authorization_pending' && <Link to="/settings">View account status</Link>}
      {!action.outcome_reason && action.outcome_reason_unavailable && (
        <span style={detail}>Action explanation unavailable.</span>
      )}
      {action.error_message ? (
        <span style={{ color: 'var(--color-status-rejected)', fontSize: '0.875rem' }}>{action.error_message}</span>
      ) : action.error_message_unavailable ? (
        <span style={detail}>An error reason was recorded for this action, but it cannot be displayed safely.</span>
      ) : null}
    </span>
  )
}

function ActionRow({ action, simulation }: { action: ActionOutcome; simulation: boolean }) {
  const { variant, label } = actionBadge(action.outcome, action.outcome_reason, simulation)
  const Icon = actionIcons[variant]
  return (
    <li style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '4px 0', flexWrap: 'wrap' }}>
      <span className={`badge badge--${variant}`}>
        <Icon size={14} aria-hidden="true" />
        {label}
      </span>
      <span>
        {action.action_type} → {action.target_playlist_id}
      </span>
      <ActionOutcomeEvidence action={action} simulation={simulation} />
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
            {decision.decision === 'skipped' && decision.skip_reason === 'exclusion' ? routeAttribution(decision) : <>{decision.route_name}{' '}<span className="route-short-id">({shortRouteId(decision.route_id)})</span></>}
          </strong>
        </div>
        <DecisionBadge decision={decision.decision} skipReason={decision.skip_reason} />
      </div>
      {decision.decision === 'skipped' && decision.skip_reason === 'exclusion' && <CausalExclusion decision={decision} />}
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
              <ActionRow key={a.id} action={a} simulation={simulation} />
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
  const [detailState, setDetailState] = useState<'loading' | 'loaded' | 'not-found' | 'error'>('loading')
  const [detailAttempt, setDetailAttempt] = useState(0)
  const [localContextRetrying, setLocalContextRetrying] = useState(false)
  const [localContextRetryError, setLocalContextRetryError] = useState(false)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const loaded = run !== null && detail !== null
  const currentDetailState = id && videoId ? detailState : 'not-found'
  async function retryLocalContext() {
    if (!id || !videoId || localContextRetrying) return
    setLocalContextRetrying(true)
    setLocalContextRetryError(false)
    try {
      const fresh = await api.getDecisionDetail(Number(id), videoId)
      if (!fresh) {
        setLocalContextRetryError(true)
        return
      }
      setDetail((current) => current ? {
        ...current,
        video: {
          ...current.video,
          current_local_metadata: fresh.video.current_local_metadata,
          metadata_context_status: fresh.video.metadata_context_status,
        },
      } : current)
    } catch {
      setLocalContextRetryError(true)
    } finally {
      setLocalContextRetrying(false)
    }
  }
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
    let active = true
    if (!id || !videoId) {
      return () => { active = false }
    }
    api.getDecisionDetail(Number(id), videoId).then((result) => {
      if (!active) return
      setDetail(result ?? null)
      setDetailState(result ? 'loaded' : 'not-found')
    }).catch(() => {
      if (active) setDetailState('error')
    })
    return () => { active = false }
  }, [id, videoId, detailAttempt])

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
  if (currentDetailState !== 'loaded' || !detail) return (
    <>
      <div className="page-header">
        <h1 className="page-title">Decision Detail</h1>
        <Link to={`/runs/${id}`} className="button">Back to run</Link>
      </div>
      <p>Run {id}</p>
      {currentDetailState === 'loading' ? <p role="status">Loading decision detail…</p> : <>
        <p role="alert">{currentDetailState === 'not-found' ? 'Decision detail not found. No routing result can be shown.' : 'Could not load decision detail. No routing result can be shown.'}</p>
        {currentDetailState === 'error' && <button className="button" onClick={() => { setDetailState('loading'); setDetailAttempt((n) => n + 1) }}>Retry</button>}
      </>}
    </>
  )

  const video = detail.video
  const mode = simulationMode(run.run_type)
  const actionTargets = new Set((detail.route_decisions ?? []).flatMap((decision) =>
    decision.actions
      .filter((action) => action.outcome_reason === 'mutation_application_uncertain')
      .map((action) => action.target_playlist_id),
  ))
  const unresolvedMutations = (video.unresolved_mutations ?? []).filter((mutation) => !actionTargets.has(mutation.target_playlist_id))
  const local = video.current_local_metadata
  const observedAt = currentLocalObservedAt(local?.source_fetched_at)
  const metadataFallback = video.metadata_context_status === 'context_read_failed'
    ? 'Could not load local video information.'
    : undefined

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
        <h2 className="section-title">{currentField(local?.title, local?.title_availability, currentLocalUnavailableLabel('title', local?.title_availability))}</h2>
        <p style={{ color: 'var(--color-text-muted)' }}>
          {local?.channel_name_availability === 'current_local' && local.channel_name != null
            ? `Source channel: ${local.channel_name}`
            : currentLocalUnavailableLabel('channel_name', local?.channel_name_availability)}
        </p>
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>Current/local information — not necessarily the information evaluated by this run.</p>
        {(local?.title_availability === 'current_local' || local?.channel_name_availability === 'current_local' || local?.duration_availability === 'current_local') && observedAt && (
          <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>{observedAt}</p>
        )}
        {video.metadata_context_status === 'context_read_failed' && (
          <div role="status" aria-live="polite" style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
            <span>Could not load local video information. Decision evidence remains available.</span>{' '}
            <button type="button" className="button" onClick={retryLocalContext} disabled={localContextRetrying}>
              {localContextRetrying ? 'Retrying…' : 'Retry local information'}
            </button>
            {localContextRetryError && <span role="alert"> Retry failed. The current/local information remains unavailable.</span>}
          </div>
        )}
        <dl className="metadata-list" style={{ marginTop: 12 }}>
          <div>
            <dt>Duration</dt>
            <dd>{local?.duration_availability === 'current_local' && local.duration_seconds != null
              ? local.duration_seconds === 0 ? '0 seconds' : formatDuration(local.duration_seconds)
              : metadataFallback ?? currentLocalUnavailableLabel('duration_seconds', local?.duration_availability)}</dd>
          </div>
          {video.published_at && (
            <div>
              <dt>Published</dt>
              <dd>{new Date(video.published_at).toLocaleDateString()}</dd>
            </div>
          )}
          <div>
            <dt>Channel ID</dt>
            <dd>{video.channel_id}</dd>
          </div>
          <div>
            <dt>Video ID</dt>
            <dd>{video.video_id}</dd>
          </div>
        </dl>
      </div>

      {unresolvedMutations.length > 0 && (
        <section className="card" aria-label="Unresolved playlist actions" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Unresolved playlist actions</h2>
          <p>Restoring YouTube access does not confirm whether these playlist actions were applied.</p>
          {unresolvedMutations.map((mutation, index) => (
            <div key={`${mutation.action_type}:${mutation.target_playlist_id}:${index}`}>
              <p>Playlist {mutation.target_playlist_id}</p>
              <p>Playlist outcome unknown</p>
              <p>YuHaul cannot confirm whether this playlist action was sent or applied. Repeating it could add the video again, so it remains blocked.</p>
              {mutation.prepared_at && <p>Prepared {new Date(mutation.prepared_at).toLocaleString()} — not an attempt time.</p>}
            </div>
          ))}
          {unresolvedMutations.length > 0 && (detail.route_decisions ?? []).every((decision) => decision.actions.length === 0) && (
            <p>An unresolved playlist action is recorded above; no action outcome was recorded.</p>
          )}
        </section>
      )}

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
        {!mode && !video.error_message && video.error_message_unavailable && (
          <p style={{ marginTop: 8, color: 'var(--color-text-muted)' }}>
            An error reason was recorded for this video, but it cannot be displayed safely.
          </p>
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
                  <ActionOutcomeBadge outcome={a.outcome} reason={a.outcome_reason} simulation={!!mode} />
                </span>
                <span style={{ marginLeft: 8 }}>
                  {a.action_type} → {a.target_playlist_id}
                </span>
                <span style={{ marginLeft: 8 }}>
                  <ActionOutcomeEvidence action={a} simulation={!!mode} />
                </span>
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
