import { Link } from 'react-router-dom'
import { Trash2 } from 'lucide-react'
import { focusOnAttach } from '../lib/focus'
import { removedRowText, retryRemovedNotice } from '../lib/retry-removed'

// Persistent notice after a Retry ended because the person's own removal won
// (design 3a). A role="alert" message, not a toast. With a row on screen it
// takes focus, because the Retry that was focused is gone; with the row already
// gone it does not, because the person's own Dismiss already placed focus.
export function RemovalAlert({ target, runId, focus }: { target?: string; runId?: number; focus: boolean }) {
  return (
    <div ref={focus ? focusOnAttach : undefined} tabIndex={-1} role="alert" className="form-error">
      {target ? `${target}: ` : ''}
      {retryRemovedNotice}
      {runId !== undefined && (
        <>
          {' '}
          <Link to={`/runs/${runId}`}>View run {runId}</Link>
        </>
      )}
    </div>
  )
}

// The removed row's action cell: neutral badge with text, no Retry, Remove or
// Dismiss, only the link to the video. It is not a failure.
export function RemovedNotice({ videoId, target }: { videoId?: string; target: string }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <span className="badge badge--not-evaluated">
        <Trash2 size={14} aria-hidden="true" />
        Removed
      </span>
      <span>{removedRowText}</span>
      {videoId && (
        <Link to={`/videos/${encodeURIComponent(videoId)}`} aria-label={`View video: ${target}`} style={{ display: 'inline-block', minHeight: 24 }}>
          View video
        </Link>
      )}
    </div>
  )
}
