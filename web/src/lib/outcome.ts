import type { VideoProcessingState, VideoFinalOutcome } from '../types'

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
