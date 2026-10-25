import { Check, X, Clock, AlertTriangle, Minus, Loader2, Ban } from 'lucide-react'
import type { RunType, VideoProcessingState, VideoFinalOutcome } from '../types'
import { deriveBadge, simulationMode } from '../lib/outcome'

const icons = {
  accepted: Check,
  rejected: X,
  excluded: Ban,
  deferred: Clock,
  failed: AlertTriangle,
  'not-evaluated': Minus,
  'in-progress': Loader2,
}

interface BadgeProps {
  state: VideoProcessingState
  finalOutcome: VideoFinalOutcome | null
  mode?: RunType
}

export function Badge({ state, finalOutcome, mode }: BadgeProps) {
  const { variant, label } = deriveBadge(state, finalOutcome, mode)
  const Icon = icons[variant]
  return (
    <>
      <span className={`badge badge--${variant}`}>
        <Icon size={14} aria-hidden="true" />
        {label}
      </span>
      {mode && simulationMode(mode) && (finalOutcome === 'deferred' || finalOutcome === 'failed') && (
        <span> Simulation evaluation {finalOutcome === 'deferred' ? 'was deferred' : 'failed'}.</span>
      )}
    </>
  )
}
