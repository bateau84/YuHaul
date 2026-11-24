import type { Ref } from 'react'
import { Link } from 'react-router-dom'
import { CircleCheck } from 'lucide-react'
import { closedText } from '../lib/final-outcome'

// A queue entry closed because its video already has a final outcome (BR-039).
// Text plus an icon, never colour alone; no Retry or Dismiss (nothing to do).
//
// linkRef is the View video link's ref; the queues use it to move focus there
// once, for the row whose Retry was just refused (useLinkFocusOnce).
export function ClosedNotice({ outcome, videoId, target, linkRef }: { outcome: string | null | undefined; videoId?: string; target: string; linkRef?: Ref<HTMLAnchorElement> }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <span className="badge badge--not-evaluated">
        <CircleCheck size={14} aria-hidden="true" />
        Closed
      </span>
      <span>{closedText(outcome)}</span>
      {videoId && (
        <Link ref={linkRef} to={`/videos/${encodeURIComponent(videoId)}`} aria-label={`View video: ${target}`} style={{ display: 'inline-block', minHeight: 24 }}>
          View video
        </Link>
      )}
    </div>
  )
}

// Persistent refusal shown where the row was. A live alert that does not take
// focus: the alert is announced once and in full, and focus goes to the closed
// row's View video link instead (ClosedNotice linkRef). A focused plain
// container was read by Orca with Chrome as its first visual line only, and an
// alert that also took focus was read twice, so this node is never focused and
// has no aria-label.
export function RefusalAlert({ text }: { text: string }) {
  return (
    <div role="alert" className="form-error">
      {text}
    </div>
  )
}
