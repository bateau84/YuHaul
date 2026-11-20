import { Link } from 'react-router-dom'
import { failureSentence, parseDiag, reasonRunLink } from '../lib/failure-reason'
import type { ReasonRun } from '../lib/failure-reason'

interface FailureReasonProps {
  /** The stored failure string as the API returned it. */
  message: string
  /** Names the disclosure: a video title, a video ID or a run label. */
  subject: string
  /**
   * Which run the reason describes, for the retry-run codes. The run's own page
   * passes `own`; a caller that has the run's id passes it; with nothing known
   * (an Errors row has no durable last-retry-run id) leave it out.
   */
  run?: ReasonRun
}

/**
 * One plain sentence for a stored failure plus, for a `diag:v1` string only, a
 * collapsed "Technical details" disclosure with the exact stored string as
 * selectable Support code text. Opening it moves no focus and announces nothing.
 */
export function FailureReason({ message, subject, run = { kind: 'unknown' } }: FailureReasonProps) {
  const supported = parseDiag(message) !== null
  const runId = reasonRunLink(message, run)
  return (
    <>
      <span>{failureSentence(message, run)}</span>
      {runId !== null && (
        <>
          {' '}
          <Link to={`/runs/${runId}`}>View run {runId}</Link>
        </>
      )}
      {supported && (
        <details className="technical-details">
          <summary aria-label={`Technical details for ${subject}`}>Technical details</summary>
          <div className="technical-details__body">
            <span className="technical-details__label">Support code</span>
            <code className="technical-details__code">{message}</code>
          </div>
        </details>
      )}
    </>
  )
}
