import { useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import * as sourceApi from '../api/source-failures'
import { useSourceFailures } from '../hooks/useSourceFailures'
import { showSourceChannelId, sourceChannelLabel, sourceReasonSentence, UPLOADS_NOT_SCANNED } from '../lib/source-failure'
import type { SourceFailure } from '../types'

// The Errors "Channel scan errors" section (discovery ARS-002 section 4.4). It
// is read-only history, separate from the video errors: no Retry, Dismiss or
// Remove, and it adds nothing to the Errors tab count.

function when(f: SourceFailure): string | null {
  if (!f.occurred_at) return null
  const d = new Date(f.occurred_at)
  return Number.isNaN(d.getTime()) ? null : d.toLocaleString()
}

export function ChannelScanErrors() {
  const list = useSourceFailures((before) => sourceApi.getSourceFailures({ beforeId: before }), 'errors')
  const firstAdded = useRef<HTMLLIElement | null>(null)

  // After "Show older entries", focus moves to the first newly added entry.
  useEffect(() => {
    if (list.added) firstAdded.current?.focus()
  }, [list.added])

  return (
    <section className="card" aria-labelledby="channel-scan-errors-heading" style={{ marginTop: 16 }}>
      <h2 id="channel-scan-errors-heading" className="section-title">Channel scan errors</h2>
      <p>
        A history of channel scans that failed, newest first. Each eligible fresh poll checks these channels again. An
        entry does not show whether a channel scans successfully now.
      </p>

      {list.load === 'loading' && list.items.length === 0 && <p role="status">Loading channel scan errors…</p>}

      {list.load === 'failed' && (
        <div role="alert" style={{ color: 'var(--color-status-failed)' }}>
          <p style={{ margin: 0 }}>Could not load channel scan errors. Nothing was changed.</p>
          {list.stale && <p style={{ margin: '4px 0 0' }}>The list below may be out of date.</p>}
          <button type="button" className="button" onClick={list.reload} style={{ marginTop: 8 }}>
            Reload channel scan errors
          </button>
        </div>
      )}

      {list.load === 'ready' && list.items.length === 0 && (
        <p>No channel scan errors are recorded. Runs from before YuHaul kept these records show none.</p>
      )}

      {list.items.length > 0 && (
        <ul style={{ listStyle: 'none', padding: 0, margin: '12px 0 0', display: 'grid', gap: 12 }}>
          {list.items.map((f) => {
            const time = when(f)
            const isFirstAdded = list.added?.firstId === f.id
            return (
              <li
                key={f.id}
                ref={isFirstAdded ? firstAdded : undefined}
                tabIndex={-1}
                style={{ borderTop: '1px solid var(--color-border-default)', paddingTop: 12, overflowWrap: 'anywhere' }}
              >
                <strong style={{ color: 'var(--color-status-failed)' }}>Channel scan error</strong>
                <div>{sourceChannelLabel(f)}</div>
                {showSourceChannelId(f) && <div>Channel ID: {f.channel_id}</div>}
                <div>
                  Failed in run {f.run_id}
                  {time ? ` on ${time}` : ''}{' '}
                  <Link to={`/runs/${f.run_id}`}>View run {f.run_id}</Link>
                </div>
                <div>
                  {UPLOADS_NOT_SCANNED} {sourceReasonSentence(f)}
                </div>
                <div style={{ color: 'var(--color-text-muted)' }}>
                  This entry records what happened in run {f.run_id}. It does not show whether this channel scans
                  successfully now.
                </div>
              </li>
            )
          })}
        </ul>
      )}

      {list.hasMore && (
        <button type="button" className="button" onClick={list.loadMore} disabled={list.loadingMore} style={{ marginTop: 12 }}>
          Show older entries
        </button>
      )}
      <p role="status" aria-live="polite" style={{ margin: '8px 0 0' }}>
        {list.added ? `${list.added.count} older ${list.added.count === 1 ? 'entry' : 'entries'} loaded.` : ''}
      </p>
    </section>
  )
}
