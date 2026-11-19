import { failureSentence, parseDiag } from '../lib/failure-reason'

interface FailureReasonProps {
  /** The stored failure string as the API returned it. */
  message: string
  /** Names the disclosure: a video title, a video ID or a run label. */
  subject: string
}

/**
 * One plain sentence for a stored failure plus, for a `diag:v1` string only, a
 * collapsed "Technical details" disclosure with the exact stored string as
 * selectable Support code text. Opening it moves no focus and announces nothing.
 */
export function FailureReason({ message, subject }: FailureReasonProps) {
  const supported = parseDiag(message) !== null
  return (
    <>
      <span>{failureSentence(message)}</span>
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
