import type { Ref } from 'react'
import { Link } from 'react-router-dom'
import { Trash2 } from 'lucide-react'
import { removedRowText, retryRemovedNotice } from '../lib/retry-removed'

// Persistent notice after a Retry ended because the person's own removal won
// (design 3a). A role="alert" message, not a toast, and never focused: it is
// announced once and in full by the alert. With a row on screen focus goes to
// the removed row's View video link instead (RemovedNotice linkRef), or to the
// list heading when there is no row; with the row already gone focus is not
// moved at all, because the person's own Dismiss already placed it.
export function RemovalAlert({ target, runId }: { target?: string; runId?: number }) {
  return (
    <div role="alert" className="form-error">
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
export function RemovedNotice({ videoId, target, linkRef }: { videoId?: string; target: string; linkRef?: Ref<HTMLAnchorElement> }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
      <span className="badge badge--not-evaluated">
        <Trash2 size={14} aria-hidden="true" />
        Removed
      </span>
      <span>{removedRowText}</span>
      {videoId && (
        <Link ref={linkRef} to={`/videos/${encodeURIComponent(videoId)}`} aria-label={`View video: ${target}`} style={{ display: 'inline-block', minHeight: 24 }}>
          View video
        </Link>
      )}
    </div>
  )
}
