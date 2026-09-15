import type { VideoOutcome, DecisionDetail, RouteDecision, ConditionAudit, ActionOutcome } from '../types'
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
}

interface VideoDetailResponse {
  video: BackendVideoOutcome & {
    route_decisions: BackendRouteDecision[]
  }
}

interface BackendRouteDecision {
  route_id: number
  route_name: string
  evaluation_order: number
  decision: RouteDecision['decision']
  skip_reason?: RouteDecision['skip_reason']
  rejection_reason?: string
  condition_audits: BackendConditionAudit[]
  executed_actions: BackendExecutedAction[]
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
  action_type: string
  target_playlist_id: string
  outcome: ActionOutcome['outcome']
  error_message?: string
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
  }
}

function toConditionAudits(audits: BackendConditionAudit[]): ConditionAudit[] {
  return audits.map((a, index) => ({
    id: index,
    condition_index: a.condition_index,
    field: a.field,
    operator: a.operator,
    expected_value: a.expected_value,
    actual_value: a.actual_value,
    matched: a.matched,
  }))
}

function toActionOutcomes(actions: BackendExecutedAction[]): ActionOutcome[] {
  return actions.map((a, index) => ({
    id: index,
    action_type: a.action_type,
    target_playlist_id: a.target_playlist_id,
    outcome: a.outcome,
    error_message: a.error_message,
  }))
}

function toRouteDecisions(decisions: BackendRouteDecision[]): RouteDecision[] {
  return decisions.map((d) => ({
    id: d.route_id,
    route_id: d.route_id,
    route_name: d.route_name,
    evaluation_order: d.evaluation_order,
    decision: d.decision,
    skip_reason: d.skip_reason,
    rejection_reason: d.rejection_reason,
    condition_audits: toConditionAudits(d.condition_audits),
    actions: toActionOutcomes(d.executed_actions),
  }))
}

export async function getRunVideos(id: number): Promise<VideoOutcome[]> {
  const data = await api<VideoListResponse>(`/api/runs/${id}/videos`)
  return data.videos.map(toVideoOutcome)
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
