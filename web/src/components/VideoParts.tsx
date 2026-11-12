import { Link } from 'react-router-dom'
import { Check, X, Clock, AlertTriangle, Minus, Loader2, Ban, CircleSlash } from 'lucide-react'
import { deriveBadge } from '../lib/outcome'
import { runLabel } from '../lib/video-copy'
import type { OutcomeBadgeVariant } from '../lib/outcome'
import type { RunType, VideoFinalOutcome, VideoQueueState } from '../types'

const icons: Record<OutcomeBadgeVariant, typeof Check> = {
  accepted: Check,
  rejected: X,
  excluded: Ban,
  deferred: Clock,
  failed: AlertTriangle,
  'not-evaluated': Minus,
  'in-progress': Loader2,
  unavailable: CircleSlash,
}

// Outcome badge for a committed outcome or a simulation run's hypothetical
// outcome. Text and icon, never colour alone.
export function OutcomeBadge({ outcome, mode }: { outcome: VideoFinalOutcome | null; mode?: RunType }) {
  const { variant, label } = deriveBadge('completed', outcome, mode)
  const Icon = icons[variant]
  return (
    <span className={`badge badge--${variant}`}>
      <Icon size={14} aria-hidden="true" />
      {label}
    </span>
  )
}

// Short queue form used in the result list (design: Queue state).
export function QueueShort({ state }: { state: VideoQueueState }) {
  switch (state.state) {
    case 'deferred_pending':
      return <>Deferred — pending</>
    case 'deferred_claimed':
      return <>Deferred — claimed by run {state.claiming_run_id ?? 'unknown'}</>
    case 'errors_active':
      return <>Errors — active</>
    case 'errors_claimed':
      return <>Errors — claimed by run {state.claiming_run_id ?? 'unknown'}</>
    case 'removed_by_user':
      return <>Removed by you</>
    default:
      return <>Not in a queue</>
  }
}

export function RunLink({ id }: { id: number }) {
  return <Link to={`/runs/${id}`}>{runLabel(id)}</Link>
}
