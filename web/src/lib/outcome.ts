import type { DeferredReason, RunType, VideoProcessingState, VideoFinalOutcome, RouteDecisionType, SkipReason } from '../types'

export type BadgeVariant = 'accepted' | 'rejected' | 'excluded' | 'deferred' | 'failed' | 'not-evaluated' | 'in-progress'

// deriveBadge alone can return the final, separate unavailable_on_youtube
// category (BR-032 AC1); route and action badges never do.
export type OutcomeBadgeVariant = BadgeVariant | 'unavailable'

// Badge/filter labels for the BR-032 outcome-category vocabulary, shared by
// the Run Detail outcome filter and the outcome badge so the two never drift.
// unavailable_on_youtube is deliberately not labelled here yet: adding it would
// add a filter option to Run Detail, and the contract change carries no UI.
export const videoOutcomeLabels: Record<Exclude<VideoFinalOutcome, 'unavailable_on_youtube'>, string> = {
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

export function deriveBadge(state: VideoProcessingState, finalOutcome: VideoFinalOutcome | null | undefined, mode?: RunType): { variant: OutcomeBadgeVariant; label: string } {
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
    // Final and its own category (BR-032 AC1): never Rejected or Failed.
    // Simulations never produce it, so the simulation branch above keeps the
    // 'Outcome unavailable' fallback.
    case 'unavailable_on_youtube': return { variant: 'unavailable', label: 'Unavailable on YouTube' }
    default: return { variant: 'not-evaluated', label: 'Outcome unavailable' }
  }
}

const skipReasonLabels: Record<SkipReason, string> = {
  final_route_matched: 'A prior final route already matched this video',
  exclusion: 'Route exclusion recorded; specific rule and compared input unavailable.',
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
      return {
        variant: 'not-evaluated',
        label: skipReason === 'exclusion' ? 'Skipped — excluded from this route.' : 'Skipped',
        reason: skipReason ? skipReasonLabels[skipReason] : undefined,
      }
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

export function actionOutcomeLabel(outcome: string, reason?: string): { variant: BadgeVariant; label: string } {
  if (outcome === 'not_attempted' && reason === 'mutation_application_uncertain') return { variant: 'not-evaluated', label: 'Not attempted — unresolved playlist action' }
  if (reason === 'mutation_application_uncertain') return { variant: 'failed', label: 'Playlist outcome unknown' }
  if (reason === 'authorization_pending') return { variant: 'deferred', label: 'Waiting for YouTube access' }
  return deriveActionOutcome(outcome)
}

// Explanatory outcome-reason vocabulary written by the action executor
// (ARD-001). `outcome_reason` explains a recorded outcome — it is never an
// outcome category. Unknown values are server-vetted safe text and render
// verbatim rather than being guessed at. `already_completed` is explicitly
// run-scoped: the prior success is not a success in this run.
const outcomeReasonLabels: Record<string, string> = {
  authorization_pending: 'Not attempted in this run. YouTube access was unavailable; the work was retained.',
  mutation_application_uncertain: 'YuHaul could not confirm whether this playlist action was applied. Repeating it could add the video again, so it remains blocked.',
  already_completed: 'Not attempted in this run — already completed by an earlier run',
  quota_exhausted: 'Not attempted — the daily quota was exhausted before this action could run',
  prior_action_failed: 'Not attempted in this run — an earlier action on this video failed',
}

export function actionOutcomeReasonLabel(reason: string): string {
  return outcomeReasonLabels[reason] ?? reason
}

export function formatDuration(seconds?: number): string {
  if (seconds === undefined || seconds === null) return '—'
  const m = Math.floor(seconds / 60)
  const s = seconds % 60
  return `${m}:${String(s).padStart(2, '0')}`
}

// Labels for the six stored deferral reasons (ARS-005 §7.3). Used by the
// Deferred reason filter; unknown stored values render verbatim.
export const deferredReasonLabels: Record<DeferredReason, string> = {
  quota_exhausted: 'Quota exhausted',
  api_rate_limited: 'Rate limited',
  authorization_pending: 'Waiting for YouTube access',
  stopped_by_user: 'Stopped by user',
  discarded_by_user: 'Discarded by user',
  requeued_by_user: 'Re-queued by user',
}
