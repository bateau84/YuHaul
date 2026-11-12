import { describe, expect, it } from 'vitest'
import { deriveBadge, deriveRouteDecision, deriveActionOutcome, actionOutcomeReasonLabel, formatDuration } from './outcome'

describe('deriveBadge', () => {
  it.each(['backtest', 'replay', 'dry_run'] as const)('uses requested-run outcomes for %s', (mode) => {
    expect(deriveBadge('evaluated', 'accepted', mode)).toEqual({ variant: 'accepted', label: 'Hypothetical accepted' })
    expect(deriveBadge('completed', 'rejected', mode)).toEqual({ variant: 'rejected', label: 'Hypothetical rejected' })
    expect(deriveBadge('completed', 'excluded', mode)).toEqual({ variant: 'excluded', label: 'Hypothetical excluded' })
    expect(deriveBadge('completed', 'not_yet_evaluated', mode)).toEqual({ variant: 'not-evaluated', label: 'Not evaluated' })
  })
  it('labels completed videos as accepted', () => {
    expect(deriveBadge('completed', 'accepted')).toEqual({ variant: 'accepted', label: 'Accepted' })
  })

  it('does not infer simulation outcomes from production state when the projection is missing', () => {
    expect(deriveBadge('completed', null, 'backtest')).toEqual({ variant: 'not-evaluated', label: 'Outcome unavailable' })
    expect(deriveBadge('completed', undefined, 'replay')).toEqual({ variant: 'not-evaluated', label: 'Outcome unavailable' })
  })

  it('uses the requested production run outcome and preserves in-progress state', () => {
    expect(deriveBadge('completed', 'deferred', 'manual')).toEqual({ variant: 'deferred', label: 'Deferred' })
    expect(deriveBadge('completed', null)).toEqual({ variant: 'not-evaluated', label: 'Outcome unavailable' })
    expect(deriveBadge('completed', 'accepted', 'manual')).toEqual({ variant: 'accepted', label: 'Accepted' })
    expect(deriveBadge('evaluated', 'accepted', 'scheduled')).toEqual({ variant: 'accepted', label: 'Accepted' })
    expect(deriveBadge('evaluating', 'accepted', 'manual')).toEqual({ variant: 'in-progress', label: 'In progress' })
  })

  it('uses the run-attributed outcome even when the mutable state differs', () => {
    expect(deriveBadge('acted', 'accepted')).toEqual({ variant: 'accepted', label: 'Accepted' })
  })

  it('labels evaluated+rejected as rejected', () => {
    expect(deriveBadge('evaluated', 'rejected')).toEqual({ variant: 'rejected', label: 'Rejected' })
  })

  it('labels evaluated+excluded as excluded, distinct from rejected and not evaluated', () => {
    expect(deriveBadge('evaluated', 'excluded')).toEqual({ variant: 'excluded', label: 'Excluded' })
  })

  it('labels deferred as deferred', () => {
    expect(deriveBadge('deferred', 'deferred')).toEqual({ variant: 'deferred', label: 'Deferred' })
  })

  it('labels not_yet_evaluated as not evaluated', () => {
    expect(deriveBadge('not_yet_evaluated', 'not_yet_evaluated')).toEqual({ variant: 'not-evaluated', label: 'Not evaluated' })
  })

  it('labels discovered/evaluating as in progress', () => {
    expect(deriveBadge('discovered', 'not_yet_evaluated')).toEqual({ variant: 'in-progress', label: 'In progress' })
    expect(deriveBadge('evaluating', 'not_yet_evaluated')).toEqual({ variant: 'in-progress', label: 'In progress' })
  })
})

describe('deriveRouteDecision', () => {
  it('labels matched routes', () => {
    expect(deriveRouteDecision('matched')).toEqual({ variant: 'accepted', label: 'Matched' })
  })

  it('labels not matched routes', () => {
    expect(deriveRouteDecision('not_matched')).toEqual({ variant: 'rejected', label: 'Did not match' })
  })

  it('labels skipped routes with reason', () => {
    expect(deriveRouteDecision('skipped', 'final_route_matched')).toEqual({
      variant: 'not-evaluated',
      label: 'Skipped',
      reason: 'A prior final route already matched this video',
    })
    expect(deriveRouteDecision('skipped', 'exclusion')).toEqual({
      variant: 'not-evaluated',
      label: 'Skipped — excluded from this route.',
      reason: 'Route exclusion recorded; specific rule and compared input unavailable.',
    })
    expect(deriveRouteDecision('skipped', 'disabled')).toEqual({
      variant: 'not-evaluated',
      label: 'Skipped',
      reason: 'This route is currently disabled',
    })
  })

  it('labels deferred routes', () => {
    expect(deriveRouteDecision('deferred')).toEqual({ variant: 'deferred', label: 'Deferred' })
  })
})

describe('deriveActionOutcome', () => {
  it('maps action outcomes', () => {
    expect(deriveActionOutcome('succeeded')).toEqual({ variant: 'accepted', label: 'Succeeded' })
    expect(deriveActionOutcome('failed')).toEqual({ variant: 'failed', label: 'Failed' })
    expect(deriveActionOutcome('deferred')).toEqual({ variant: 'deferred', label: 'Deferred' })
    expect(deriveActionOutcome('not_attempted')).toEqual({ variant: 'not-evaluated', label: 'Not attempted' })
  })
})

describe('actionOutcomeReasonLabel', () => {
  it('renders the recorded no-attempt reasons as run-scoped evidence', () => {
    expect(actionOutcomeReasonLabel('already_completed')).toBe(
      'Not attempted in this run — already completed by an earlier run',
    )
    expect(actionOutcomeReasonLabel('quota_exhausted')).toBe(
      'Not attempted — the daily quota was exhausted before this action could run',
    )
    expect(actionOutcomeReasonLabel('prior_action_failed')).toBe(
      'Not attempted in this run — an earlier action on this video failed',
    )
  })

  // Unknown reasons are server-vetted safe text: render verbatim, never
  // invent an interpretation.
  it('renders an unknown reason verbatim', () => {
    expect(actionOutcomeReasonLabel('some_future_reason')).toBe('some_future_reason')
  })
})

describe('formatDuration', () => {
  it('formats seconds as m:ss', () => {
    expect(formatDuration(184)).toBe('3:04')
    expect(formatDuration(62)).toBe('1:02')
    expect(formatDuration(0)).toBe('0:00')
  })

  it('returns em dash for missing values', () => {
    expect(formatDuration(undefined)).toBe('—')
  })
})

describe('deriveBadge unavailable_on_youtube (BR-032 AC1)', () => {
  it('is its own final category for production runs', () => {
    expect(deriveBadge('completed', 'unavailable_on_youtube', 'scheduled')).toEqual({ variant: 'unavailable', label: 'Unavailable on YouTube' })
    expect(deriveBadge('evaluated', 'unavailable_on_youtube')).toEqual({ variant: 'unavailable', label: 'Unavailable on YouTube' })
  })

  it('falls back for a simulation row, which never produces it', () => {
    expect(deriveBadge('completed', 'unavailable_on_youtube', 'dry_run')).toEqual({ variant: 'not-evaluated', label: 'Outcome unavailable' })
  })
})
