import type { VideoOutcome, DecisionDetail, RouteDecision, ConditionAudit, ActionOutcome, RouteActionType } from '../types'
import { api } from './core'

interface VideoListResponse {
  videos: BackendVideoOutcome[]
  pagination: {
    page: number
    page_size: number
    total: number
    total_pages: number
  }
}

interface BackendVideoOutcome {
  id: number
  video_id: string
  channel_id: string
  title?: string
  channel_name?: string
  duration_seconds?: number
  published_at?: string
  thumbnail_url?: string
  processing_state: VideoOutcome['processing_state']
  final_outcome: VideoOutcome['final_outcome']
  deferred_reason?: string
  error_message?: string
  error_message_unavailable?: boolean
  current_local_metadata?: VideoOutcome['current_local_metadata']
  metadata_context_status?: VideoOutcome['metadata_context_status']
  unresolved_mutations?: VideoOutcome['unresolved_mutations']
  route_decisions?: BackendRouteDecision[] | null
}

interface VideoDetailResponse {
  video: BackendVideoOutcome & {
    route_decisions: BackendRouteDecision[]
  }
}

interface BackendRouteDecision {
  // Public route identity is the stable UUID string (ARS-004/ARS-001); the
  // internal integer route key is never emitted on this surface.
  route_id: string
  route_name: string
  route_name_source?: string
  causal_exclusion?: RouteDecision['causal_exclusion']
  failed_condition?: RouteDecision['failed_condition']
  evaluation_order: number
  decision: RouteDecision['decision']
  skip_reason?: RouteDecision['skip_reason']
  rejection_reason?: string
  condition_audits?: BackendConditionAudit[] | null
  executed_actions?: BackendExecutedAction[] | null
}

interface BackendConditionAudit {
  condition_index: number
  field: string
  operator: string
  expected_value: string
  actual_value?: string
  matched: boolean
}

interface BackendExecutedAction {
  action_type: RouteActionType
  target_playlist_id: string
  outcome: ActionOutcome['outcome']
  outcome_reason?: string
  outcome_reason_unavailable?: boolean
  error_message?: string
  error_message_unavailable?: boolean
  executed_at?: string
}

function toVideoOutcome(v: BackendVideoOutcome): VideoOutcome {
  return {
    id: v.id,
    video_id: v.video_id,
    channel_id: v.channel_id,
    title: v.title,
    channel_name: v.channel_name,
    duration_seconds: v.duration_seconds,
    published_at: v.published_at,
    thumbnail_url: v.thumbnail_url,
    processing_state: v.processing_state,
    final_outcome: v.final_outcome,
    deferred_reason: v.deferred_reason,
    error_message: v.error_message,
    error_message_unavailable: v.error_message_unavailable,
    current_local_metadata: v.current_local_metadata,
    metadata_context_status: v.metadata_context_status,
    unresolved_mutations: v.unresolved_mutations,
    route_decisions: v.route_decisions?.map(toRouteDecisionSummary),
  }
}

function toConditionAudits(audits: BackendConditionAudit[] | null | undefined): ConditionAudit[] {
  return (audits ?? []).map((a, index) => ({
    id: index,
    condition_index: a.condition_index,
    field: a.field,
    operator: a.operator,
    expected_value: a.expected_value,
    actual_value: a.actual_value,
    matched: a.matched,
  }))
}

function toActionOutcomes(actions: BackendExecutedAction[] | null | undefined): ActionOutcome[] {
  return (actions ?? []).map((a, index) => ({
    id: index,
    action_type: a.action_type,
    target_playlist_id: a.target_playlist_id,
    outcome: a.outcome,
    outcome_reason: a.outcome_reason,
    outcome_reason_unavailable: a.outcome_reason_unavailable,
    error_message: a.error_message,
    error_message_unavailable: a.error_message_unavailable,
    executed_at: a.executed_at,
  }))
}

function toRouteDecisionSummary(d: BackendRouteDecision) {
  return {
    id: d.route_id,
    route_id: d.route_id,
    route_name: d.route_name,
    route_name_source: d.route_name_source,
    causal_exclusion: d.causal_exclusion,
    failed_condition: d.failed_condition,
    evaluation_order: d.evaluation_order,
    decision: d.decision,
    skip_reason: d.skip_reason,
    rejection_reason: d.rejection_reason,
  }
}

function toRouteDecisions(decisions: BackendRouteDecision[] | null | undefined): RouteDecision[] {
  return (decisions ?? []).map((d) => ({
    ...toRouteDecisionSummary(d),
    condition_audits: toConditionAudits(d.condition_audits),
    actions: toActionOutcomes(d.executed_actions),
  }))
}

// RunVideoPage is one page of a run's video outcomes plus the server-side
// total and page bounds for that (possibly outcome-filtered) set.
export interface RunVideoPage {
  videos: VideoOutcome[]
  total: number
  /** Total pages available for the current filter and page size. */
  totalPages: number
}

export interface RunVideoQuery {
  /** final_outcome category; narrows the server-side result and totals. */
  outcome?: VideoOutcome['final_outcome']
  /** page size requested from the server (server max is 100). */
  pageSize?: number
  /** 1-based page to fetch; omitted means the server default (page 1). */
  page?: number
}

export async function getRunVideos(id: number, query: RunVideoQuery = {}): Promise<RunVideoPage> {
  const params = new URLSearchParams()
  if (query.outcome) params.set('outcome', query.outcome)
  if (query.pageSize) params.set('page_size', String(query.pageSize))
  if (query.page && query.page > 1) params.set('page', String(query.page))
  const search = params.toString()
  const data = await api<VideoListResponse>(`/api/runs/${id}/videos${search ? `?${search}` : ''}`)
  return {
    videos: data.videos.map(toVideoOutcome),
    total: data.pagination?.total ?? data.videos.length,
    totalPages: data.pagination?.total_pages ?? 1,
  }
}

export async function getDecisionDetail(
  runId: number,
  videoId: string,
): Promise<DecisionDetail | undefined> {
  try {
    const data = await api<VideoDetailResponse>(`/api/runs/${runId}/videos/${videoId}`)
    return {
      video: toVideoOutcome(data.video),
      processing_state: data.video.processing_state,
      final_outcome: data.video.final_outcome,
      route_decisions: toRouteDecisions(data.video.route_decisions),
    }
  } catch (err) {
    if (err instanceof Error && err.message.includes('not found')) {
      return undefined
    }
    throw err
  }
}
