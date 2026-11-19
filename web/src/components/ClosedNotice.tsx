import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import { CircleCheck } from 'lucide-react'
import { closedText } from '../lib/final-outcome'

// A queue entry closed because its video already has a final outcome (BR-039).
// Text plus an icon, never colour alone; no Retry or Dismiss (nothing to do).
export function ClosedNotice({ outcome, videoId, target }: { outcome: string | null | undefined; videoId?: string; target: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <span className="badge badge--not-evaluated">
        <CircleCheck size={14} aria-hidden="true" />
        Closed
      </span>
      <span>{closedText(outcome)}</span>
      {videoId && (
        <Link to={`/videos/${encodeURIComponent(videoId)}`} aria-label={`View video: ${target}`} style={{ display: 'inline-block', minHeight: 24 }}>
          View video
        </Link>
      )}
    </div>
  )
}

// Persistent refusal shown where the row was. The Retry button that was
// focused is gone, so focus moves to the alert instead of falling to <body>.
export function RefusalAlert({ text }: { text: string }) {
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    ref.current?.focus()
  }, [])
  return (
    <div ref={ref} tabIndex={-1} role="alert" className="form-error">
      {text}
    </div>
  )
}
