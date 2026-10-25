import type { RunType, VideoProcessingState, VideoFinalOutcome, RouteDecisionType, SkipReason } from '../types'

export type BadgeVariant = 'accepted' | 'rejected' | 'excluded' | 'deferred' | 'failed' | 'not-evaluated' | 'in-progress'

// Badge/filter labels for the BR-032 outcome-category vocabulary, shared by
// the Run Detail outcome filter and the outcome badge so the two never drift.
export const videoOutcomeLabels: Record<VideoFinalOutcome, string> = {
  accepted: 'Accepted',
  rejected: 'Rejected',
  excluded: 'Excluded',
  deferred: 'Deferred',
  failed: 'Failed',
  not_yet_evaluated: 'Not evaluated',
}

export const simulationExplanation = 'Simulation only. Results are hypothetical; no playlist actions were executed by this run.'

export function simulationMode(mode: RunType): string | null {
  switch (mode) {
    case 'backtest': return 'Backtest'
    case 'replay': return 'Replay'
    case 'dry_run': return 'Dry-run'
    default: return null
  }
}

export function deriveBadge(state: VideoProcessingState, finalOutcome: VideoFinalOutcome | null | undefined, mode?: RunType): { variant: BadgeVariant; label: string } {
  if (mode && simulationMode(mode)) {
    switch (finalOutcome) {
      case 'accepted': return { variant: 'accepted', label: 'Hypothetical accepted' }
      case 'rejected': return { variant: 'rejected', label: 'Hypothetical rejected' }
      case 'excluded': return { variant: 'excluded', label: 'Hypothetical excluded' }
      case 'deferred': return { variant: 'deferred', label: 'Deferred' }
      case 'failed': return { variant: 'failed', label: 'Failed' }
      case 'not_yet_evaluated': return { variant: 'not-evaluated', label: 'Not evaluated' }
      default: return { variant: 'not-evaluated', label: 'Outcome unavailable' }
    }
  }
  if (state === 'discovered' || state === 'evaluating') return { variant: 'in-progress', label: 'In progress' }
  switch (finalOutcome) {
    case 'accepted': return { variant: 'accepted', label: 'Accepted' }
    case 'rejected': return { variant: 'rejected', label: 'Rejected' }
    case 'excluded': return { variant: 'excluded', label: 'Excluded' }
    case 'deferred': return { variant: 'deferred', label: 'Deferred' }
    case 'failed': return { variant: 'failed', label: 'Failed' }
    case 'not_yet_evaluated': return { variant: 'not-evaluated', label: 'Not evaluated' }
    default: return { variant: 'not-evaluated', label: 'Outcome unavailable' }
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
