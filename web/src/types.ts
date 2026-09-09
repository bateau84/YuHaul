// Shared YuHaul Admin SPA types, aligned with the backend contract and design spec.

export type Composition = 'AND' | 'OR'

export type ConditionField =
  | 'title'
  | 'channel_id'
  | 'channel_name'
  | 'description'
  | 'duration_seconds'

export type ConditionOperator =
  | 'contains'
  | 'not_contains'
  | 'equals'
  | 'not_equals'
  | 'gt'
  | 'gte'
  | 'lt'
  | 'lte'
  | 'in_list'

export type ExclusionType =
  | 'channel'
  | 'video'
  | 'title'
  | 'word'
  | 'duration_min'
  | 'duration_max'

export interface RouteCondition {
  id?: number
  route_id?: number
  condition_index: number
  field: ConditionField
  operator: ConditionOperator
  value: string
}

export interface RouteAction {
  id?: number
  route_id?: number
  action_type: 'playlist_insert'
  target_playlist_id: string
  action_index: number
}

export interface RouteExclusion {
  id?: number
  route_id?: number
  exclusion_type: ExclusionType
  exclusion_value: string
}

export interface Route {
  id: number
  name: string
  description: string
  evaluation_order: number
  is_final: boolean
  is_enabled: boolean
  condition_composition: Composition
  conditions: RouteCondition[]
  actions: RouteAction[]
  exclusions: RouteExclusion[]
  created_at: string
  updated_at: string
}

export type RunType =
  | 'scheduled'
  | 'manual'
  | 'dry_run'
  | 'replay'
  | 'backtest'

export type RunStatus = 'running' | 'completed' | 'interrupted'

export interface Run {
  id: number
  started_at: string
  completed_at?: string
  run_type: RunType
  status: RunStatus
  config_version_id?: number
  videos_discovered: number
  videos_accepted: number
  videos_rejected: number
  videos_deferred: number
  videos_failed: number
  videos_not_yet_evaluated: number
  quota_cost: number
  duration_seconds?: number
  interrupt_reason?: string
  current_phase?: string
}

export type VideoProcessingState =
  | 'discovered'
  | 'evaluating'
  | 'evaluated'
  | 'acted'
  | 'completed'
  | 'deferred'
  | 'failed'
  | 'not_yet_evaluated'

export type VideoFinalOutcome =
  | 'accepted'
  | 'rejected'
  | 'deferred'
  | 'failed'
  | 'not_yet_evaluated'

export interface VideoOutcome {
  id: number
  video_id: string
  channel_id: string
  title?: string
  channel_name?: string
  duration_seconds?: number
  published_at?: string
  thumbnail_url?: string
  processing_state: VideoProcessingState
  final_outcome: VideoFinalOutcome
  deferred_reason?: string
  error_message?: string
}

export type RouteDecisionType =
  | 'matched'
  | 'not_matched'
  | 'deferred'
  | 'skipped'

export type SkipReason =
  | 'final_route_matched'
  | 'exclusion'
  | 'disabled'

export interface ConditionAudit {
  id: number
  condition_index: number
  field: string
  operator: string
  expected_value: string
  actual_value?: string
  matched: boolean
}

export interface ActionOutcome {
  id: number
  action_type: string
  target_playlist_id: string
  outcome: 'succeeded' | 'failed' | 'deferred' | 'not_attempted'
  error_message?: string
}

export interface RouteDecision {
  id: number
  route_id: number
  route_name: string
  evaluation_order: number
  decision: RouteDecisionType
  skip_reason?: SkipReason
  rejection_reason?: string
  condition_audits: ConditionAudit[]
  actions: ActionOutcome[]
}

export interface DecisionDetail {
  video: VideoOutcome
  processing_state: VideoProcessingState
  final_outcome: VideoFinalOutcome
  route_decisions: RouteDecision[]
}

export interface QuotaBudget {
  daily_limit: number
  consumed_today: number
  remaining: number
  reset_at?: string
}

export interface QuotaBreakdown {
  operation_type: string
  units_consumed: number
  call_count: number
}

export interface ChannelStats {
  channel_id: string
  channel_name: string
  total_discovered: number
  total_routed: number
  total_retained: number
  usefulness_rate: number
  yield: number
  last_useful_discovery?: string
  advisories?: Advisory[]
}

export interface ChannelDetail extends ChannelStats {
  subscription_date?: string
  duplicate_provenance: {
    discovery_credit: number
    relevance_credit: number
  }
  retention_signals: {
    user_driven: number
    yuhaul_routed: number
  }
  recent_videos: VideoOutcome[]
  matched_routes: string[]
}

export type AdvisoryType =
  | 'low_usefulness'
  | 'declining_yield'
  | 'high_duplicate_rate'

export interface Advisory {
  id: number
  channel_id: string
  advisory_type: AdvisoryType
  generated_at: string
  status: 'active' | 'dismissed' | 'ignored' | 'acted_upon'
  ignored_until?: string
  action_taken?: string
  evidence_snapshot?: Record<string, unknown>
}

export interface TrendBucket {
  bucket: string
  discovered: number
  accepted: number
  rejected: number
  deferred: number
  failed: number
}

export interface ConfigVersion {
  id: number
  created_at: string
  snapshot: {
    routes: Route[]
    duplicate_threshold: number
    duplicate_behavior: string
  }
  change_description?: string
}

export interface Settings {
  schedule: string
  quota_limit: number
  duplicate_threshold: number
  duplicate_behavior: string
  account_connected: boolean
  account_email?: string
}

export interface RetentionPlaylist {
  id: number
  playlist_id: string
  playlist_name: string
  designated_at: string
  is_active: boolean
}

export interface ErrorEntry {
  id: number
  video_id: number
  video_title?: string
  channel_name?: string
  run_id: number
  error_category: 'api_error' | 'authorization' | 'runtime'
  error_message: string
  status: 'active' | 'retrying' | 'dismissed' | 'resolved'
  created_at: string
  retry_count: number
}

export interface DeferredItem {
  id: number
  video_id: number
  video_title?: string
  channel_name?: string
  run_id: number
  deferral_reason: string
  retry_after?: string
  status: 'pending' | 'retrying' | 'completed' | 'discarded'
  created_at: string
  retry_count: number
}

export type ToastType = 'success' | 'error' | 'info'

export interface Toast {
  id: string
  type: ToastType
  message: string
}
