import { Link } from 'react-router-dom'
import { CircleCheck } from 'lucide-react'
import { closedText } from '../lib/final-outcome'

// A queue entry closed because its video already has a final outcome (BR-039).
// Text plus an icon, never colour alone; no Retry or Dismiss (nothing to do).
export function ClosedNotice({ outcome, videoId }: { outcome: string | null | undefined; videoId?: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <span className="badge badge--not-evaluated">
        <CircleCheck size={14} aria-hidden="true" />
        Closed
      </span>
      <span>{closedText(outcome)}</span>
      {videoId && (
        <Link to={`/videos/${encodeURIComponent(videoId)}`} style={{ display: 'inline-block', minHeight: 24 }}>
          View video
        </Link>
      )}
    </div>
  )
}

// Persistent refusal shown where the row was; no focus move (SC 4.1.3).
export function RefusalAlert({ text }: { text: string }) {
  return (
    <div role="alert" className="form-error">
      {text}
    </div>
  )
}
