import type { VideoProcessingState, VideoFinalOutcome, RouteDecisionType, SkipReason } from '../types'

export type BadgeVariant = 'accepted' | 'rejected' | 'deferred' | 'failed' | 'not-evaluated' | 'in-progress'

export function deriveBadge(state: VideoProcessingState, finalOutcome: VideoFinalOutcome): { variant: BadgeVariant; label: string } {
  switch (state) {
    case 'completed':
      return { variant: 'accepted', label: 'Accepted' }
    case 'acted':
      return { variant: 'failed', label: 'Failed' }
    case 'evaluated':
      return finalOutcome === 'rejected' ? { variant: 'rejected', label: 'Rejected' } : { variant: 'not-evaluated', label: 'Not evaluated' }
    case 'deferred':
      return { variant: 'deferred', label: 'Deferred' }
    case 'not_yet_evaluated':
      return { variant: 'not-evaluated', label: 'Not evaluated' }
    case 'discovered':
    case 'evaluating':
      return { variant: 'in-progress', label: 'In progress' }
    case 'failed':
      return { variant: 'failed', label: 'Failed' }
    default:
      return { variant: 'not-evaluated', label: 'Not evaluated' }
  }
}

const skipReasonLabels: Record<SkipReason, string> = {
  final_route_matched: 'A prior final route already matched this video',
  exclusion: 'This video matched a route-scoped exclusion rule',
  disabled: 'This route is currently disabled',
}

export function deriveRouteDecision(decision: RouteDecisionType, skipReason?: SkipReason): { variant: BadgeVariant; label: string; reason?: string } {
  switch (decision) {
    case 'matched':
      return { variant: 'accepted', label: 'Matched' }
    case 'not_matched':
      return { variant: 'rejected', label: 'Did not match' }
    case 'deferred':
      return { variant: 'deferred', label: 'Deferred' }
    case 'skipped':
      return { variant: 'not-evaluated', label: 'Skipped', reason: skipReason ? skipReasonLabels[skipReason] : undefined }
    default:
      return { variant: 'not-evaluated', label: decision }
  }
}

export function deriveActionOutcome(outcome: string): { variant: BadgeVariant; label: string } {
  switch (outcome) {
    case 'succeeded':
      return { variant: 'accepted', label: 'Succeeded' }
    case 'failed':
      return { variant: 'failed', label: 'Failed' }
    case 'deferred':
      return { variant: 'deferred', label: 'Deferred' }
    case 'not_attempted':
      return { variant: 'not-evaluated', label: 'Not attempted' }
    default:
      return { variant: 'not-evaluated', label: outcome }
  }
}

export function formatDuration(seconds?: number): string {
  if (seconds === undefined || seconds === null) return '—'
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}
