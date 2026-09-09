import { Check, X, Clock, AlertTriangle, Minus, Loader2 } from 'lucide-react'
import type { VideoProcessingState, VideoFinalOutcome } from '../types'
import { deriveBadge } from '../lib/outcome'

const icons = {
  accepted: Check,
  rejected: X,
  deferred: Clock,
  failed: AlertTriangle,
  'not-evaluated': Minus,
  'in-progress': Loader2,
}

interface BadgeProps {
  state: VideoProcessingState
  finalOutcome: VideoFinalOutcome
}

export function Badge({ state, finalOutcome }: BadgeProps) {
  const { variant, label } = deriveBadge(state, finalOutcome)
  const Icon = icons[variant]
  return (
    <span className={`badge badge--${variant}`}>
      <Icon size={14} aria-hidden="true" />
      {label}
    </span>
  )
}
