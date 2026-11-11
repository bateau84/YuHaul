import { useCallback, useEffect, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { useSearchParams } from 'react-router-dom'
import * as api from '../api/client'
import type { DeferredPageQuery } from '../api/deferred-page'
import { deferredReasonLabels } from '../lib/outcome'
import { deferredReasons } from '../types'
import type { DeferredItem, DeferredPage, DeferredReason } from '../types'
import { ConfirmDialog } from './ConfirmDialog'
import { FetchErrorRegion } from './FetchErrorRegion'
import { useToast } from '../hooks/useToast'

const PAGE_SIZE = 50
const nf = new Intl.NumberFormat()
const fmt = (n: number) => nf.format(n)

// ---- URL state (ARS-005 §7.3) ------------------------------------------------
// The URL holds the position as the API defines it (page, after_id or before_id)
// and the filter by its stored reason value.

type Position =
  | { kind: 'first' }
  | { kind: 'page'; page: number | 'last' }
  | { kind: 'after'; id: number }
  | { kind: 'before'; id: number }

const positiveInt = (s: string | null): number | null => (s !== null && /^[1-9]\d{0,14}$/.test(s) ? Number(s) : null)

function readPosition(sp: URLSearchParams): Position {
  const page = sp.get('page')
  if (page === 'last') return { kind: 'page', page: 'last' }
  const p = positiveInt(page)
  if (p !== null) return { kind: 'page', page: p }
  const after = positiveInt(sp.get('after_id'))
  if (after !== null) return { kind: 'after', id: after }
  const before = positiveInt(sp.get('before_id'))
  if (before !== null) return { kind: 'before', id: before }
  return { kind: 'first' }
}

function isReason(v: string): v is DeferredReason {
  return (deferredReasons as readonly string[]).includes(v)
}

function toQuery(pos: Position, reason: DeferredReason | undefined): DeferredPageQuery {
  const q: DeferredPageQuery = { limit: PAGE_SIZE, reason }
  if (pos.kind === 'page') q.page = pos.page
  if (pos.kind === 'after') q.afterId = pos.id
  if (pos.kind === 'before') q.beforeId = pos.id
  return q
}

// ---- Row text ----------------------------------------------------------------

function isHandedOver(d: DeferredItem): boolean {
  return d.deferral_reason === 'stopped_by_user' || d.deferral_reason === 'discarded_by_user'
}

// A re-queued item keeps its stored reason after a run claims and defers it
// again, so a future retry_after means it is not eligible now (design: known
// limitation of "Re-queued by user").
function notEligibleYet(d: DeferredItem): boolean {
  return d.deferral_reason === 'requeued_by_user' && !!d.retry_after && new Date(d.retry_after).getTime() > Date.now()
}

function reasonText(d: DeferredItem): string {
  if (d.deferred_reason_unavailable) return 'Waiting for the next run.'
  if (isHandedOver(d)) {
    const what = d.deferral_reason === 'stopped_by_user' ? 'stopped by user' : 'discarded'
    return `Waiting for the next run. Run ${d.run_id} was ${what}.`
  }
  if (!isReason(d.deferral_reason)) return 'Waiting for the next run.'
  const label = deferredReasonLabels[d.deferral_reason]
  return notEligibleYet(d) ? `${label} Not eligible yet.` : label
}

function eligibilityText(d: DeferredItem): string {
  if (d.retry_after) return new Date(d.retry_after).toLocaleString()
  return isHandedOver(d) || d.deferral_reason === 'requeued_by_user' ? 'Eligible for the next run' : 'Next run'
}

// ---- Component ---------------------------------------------------------------

interface Props {
  /** Only the active tab renders; the page is still loaded so the tab label can show the queue total. */
  active: boolean
  /** Reports the whole-queue total (never the filtered count), or null before the first load. */
  onQueueTotal: (total: number | null) => void
}

type Focus = { kind: 'row'; index: number } | { kind: 'heading' }

// What to do once the next read settles: where focus goes and whether to
// announce the page. It is bound to the read that starts after the action, so a
// render of older data cannot consume it.
interface Intent {
  focus: Focus
  announce: boolean
}

export function DeferredQueue({ active, onQueueTotal }: Props) {
  const [searchParams, setSearchParams] = useSearchParams()
  const { addToast } = useToast()

  const position = readPosition(searchParams)
  const reasonParam = searchParams.get('reason')
  const reason = reasonParam !== null && isReason(reasonParam) ? reasonParam : undefined
  const unknownReason = reasonParam !== null && reason === undefined
  const query = toQuery(position, reason)
  const queryKey = JSON.stringify(query)

  const [data, setData] = useState<DeferredPage | null>(null)
  const [status, setStatus] = useState<'loading' | 'error' | 'ready'>('loading')
  const [reloads, setReloads] = useState(0)
  const [reasonDraft, setReasonDraft] = useState<string>(reason ?? '')
  const [jumpValue, setJumpValue] = useState('')
  const [jumpError, setJumpError] = useState<string | null>(null)
  const [announcement, setAnnouncement] = useState('')
  const [confirmDismiss, setConfirmDismiss] = useState<{ item: DeferredItem; index: number } | null>(null)

  const headingRef = useRef<HTMLHeadingElement>(null)
  const bodyRef = useRef<HTMLTableSectionElement>(null)
  const retryRef = useRef<HTMLButtonElement>(null)
  const intentRef = useRef<Intent | null>(null)
  const settledIntent = useRef<Intent | null>(null)
  const loadedKey = useRef<string | null>(null)
  const queryRef = useRef(query)
  queryRef.current = query

  useEffect(() => setReasonDraft(reason ?? ''), [reason])

  useEffect(() => {
    let cancelled = false
    // A new position or filter replaces the list with a loading state; a reload
    // of the same position (after Dismiss) keeps the list on screen meanwhile.
    if (loadedKey.current !== queryKey) setStatus('loading')
    const intent = intentRef.current
    intentRef.current = null
    api
      .getDeferredPage(queryRef.current)
      .then((page) => {
        if (cancelled) return
        loadedKey.current = queryKey
        const pastEnd = page.deferred.length === 0 && page.total > 0
        if (pastEnd && intent && queryRef.current.page !== 'last') {
          // The position fell off the end after a removal: go to the last page.
          setSearchParams(
            (prev) => {
              const next = new URLSearchParams(prev)
              next.delete('after_id')
              next.delete('before_id')
              next.set('page', 'last')
              return next
            },
            { replace: true },
          )
          intentRef.current = { focus: { kind: 'heading' }, announce: true }
          return
        }
        settledIntent.current = intent
        setData(page)
        setStatus('ready')
        onQueueTotal(page.queue_total)
      })
      .catch(() => {
        if (cancelled) return
        setStatus('error')
      })
    return () => {
      cancelled = true
    }
    // onQueueTotal and setSearchParams are stable enough for this purpose; the
    // request is driven by the position, filter and reload counter only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [queryKey, reloads])

  // Focus and announce once a navigation or Dismiss has settled.
  useEffect(() => {
    if (status !== 'ready' || !data || !active) return
    const intent = settledIntent.current
    settledIntent.current = null
    if (!intent) return
    const { focus } = intent
    const row = focus.kind === 'row' ? bodyRef.current?.rows[focus.index] : undefined
    if (row) row.focus()
    else headingRef.current?.focus()
    if (intent.announce) {
      const { number, total_pages, first_position, last_position } = data.page
      setAnnouncement(
        first_position === null
          ? `Page ${number} of ${total_pages}, no items.`
          : `Page ${number} of ${total_pages}, showing ${fmt(first_position)}–${fmt(last_position ?? first_position)}.`,
      )
    }
  }, [status, data, active])

  useEffect(() => {
    if (status === 'error' && active) retryRef.current?.focus()
  }, [status, active])

  const go = useCallback(
    (set: (p: URLSearchParams) => void) => {
      intentRef.current = { focus: { kind: 'heading' }, announce: true }
      setJumpError(null)
      setSearchParams((prev) => {
        const next = new URLSearchParams(prev)
        next.delete('page')
        next.delete('after_id')
        next.delete('before_id')
        set(next)
        return next
      })
    },
    [setSearchParams],
  )

  function applyFilter() {
    go((p) => {
      if (reasonDraft) p.set('reason', reasonDraft)
      else p.delete('reason')
    })
  }

  function clearFilter() {
    setReasonDraft('')
    go((p) => p.delete('reason'))
  }

  function submitJump(e: FormEvent) {
    e.preventDefault()
    const pages = data?.page.total_pages ?? 1
    const n = positiveInt(jumpValue.trim())
    if (n === null || n > pages) {
      setJumpError(`Enter a page from 1 to ${fmt(pages)}.`)
      return
    }
    setJumpValue('')
    go((p) => p.set('page', String(n)))
  }

  function retryLoad() {
    setStatus('loading')
    setReloads((n) => n + 1)
  }

  // Dismiss and manual retry reload the same position (same page, cursor and filter).
  async function removeItem(index: number, action: () => Promise<unknown>, done: string, failed: string) {
    try {
      await action()
      addToast(done, 'success')
      intentRef.current = { focus: { kind: 'row', index }, announce: false }
      setReloads((n) => n + 1)
    } catch (err) {
      addToast(err instanceof Error ? err.message : failed, 'error')
    }
  }

  const discard = (d: DeferredItem, index: number) =>
    removeItem(index, () => api.discardDeferred(d.id), 'Deferred item dismissed', 'Failed to discard deferred item')

  if (!active) return null

  if (status === 'error') {
    return (
      <FetchErrorRegion
        ref={retryRef}
        message="Could not load the deferred queue. Nothing was changed."
        onRetry={retryLoad}
        retrying={false}
      />
    )
  }

  if (status === 'loading' || !data) {
    return (
      <div className="card" role="status">
        <div className="empty-state">Loading deferred items…</div>
      </div>
    )
  }

  const { page } = data
  const items = data.deferred
  const filtered = reason !== undefined
  const pastEnd = items.length === 0 && data.total > 0
  const queueEmpty = data.queue_total === 0
  const first = page.first_position
  const last = page.last_position
  const showFirst = page.number > 1 && !pastEnd
  const showPrev = page.prev_before_id !== null
  const showNext = page.next_after_id !== null
  const showLast = page.number < page.total_pages
  const showNav = showFirst || showPrev || showNext || showLast || page.total_pages > 1

  return (
    <div className="card">
      <h2 ref={headingRef} tabIndex={-1} style={{ marginTop: 0, fontSize: '1.1rem' }}>
        Deferred queue
      </h2>
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          applyFilter()
        }}
        style={{ display: 'flex', gap: 8, alignItems: 'end', flexWrap: 'wrap', marginBottom: 12 }}
      >
        <div>
          <label htmlFor="deferred-reason" style={{ display: 'block' }}>
            Reason
          </label>
          <select
            id="deferred-reason"
            className="form-select"
            value={reasonDraft}
            onChange={(e) => setReasonDraft(e.target.value)}
          >
            <option value="">All reasons</option>
            {deferredReasons.map((r) => (
              <option key={r} value={r}>
                {deferredReasonLabels[r]}
              </option>
            ))}
          </select>
        </div>
        <button type="submit" className="button">
          Apply filter
        </button>
        <button type="button" className="button" onClick={clearFilter}>
          Clear filter
        </button>
      </form>
      {unknownReason && <p>Unknown reason filter. Showing all reasons.</p>}

      {queueEmpty ? (
        <div className="empty-state">No deferred work. All pending items have been processed.</div>
      ) : pastEnd ? (
        <div role="status">
          <p>This page no longer exists. The queue has changed.</p>
          <button type="button" className="button" onClick={() => go((p) => p.set('page', 'last'))}>
            Go to the last page
          </button>
        </div>
      ) : items.length === 0 ? (
        <div className="empty-state">No deferred items have this reason.</div>
      ) : (
        <>
          <p>
            {filtered
              ? `Showing ${fmt(first ?? 0)}–${fmt(last ?? 0)} of ${fmt(data.total)} items with this reason. ${fmt(data.queue_total)} items in the deferred queue.`
              : `Showing ${fmt(first ?? 0)}–${fmt(last ?? 0)} of ${fmt(data.total)} items.`}
          </p>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Video</th>
                <th scope="col">Reason</th>
                <th scope="col">Retry after</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody ref={bodyRef}>
              {items.map((d, i) => (
                <tr key={d.id} tabIndex={-1}>
                  <td data-label="Video">
                    {d.video_title ?? (
                      <>
                        Video title unavailable <span style={{ color: 'var(--color-text-muted)' }}>Video ID: {d.video_id}</span>
                      </>
                    )}
                  </td>
                  <td data-label="Reason">{reasonText(d)}</td>
                  <td data-label="Retry after">{eligibilityText(d)}</td>
                  <td data-label="Actions">
                    <div style={{ display: 'flex', gap: 8 }}>
                      <button
                        className="button"
                        onClick={() =>
                          removeItem(
                            i,
                            () => api.retryDeferred(d.id),
                            'Deferred item queued for retry',
                            'Failed to retry deferred item',
                          )
                        }
                      >
                        Retry now
                      </button>
                      <button
                        className="button"
                        aria-label={`Dismiss deferred item: ${d.video_title ?? d.video_id}`}
                        onClick={() => (isHandedOver(d) ? setConfirmDismiss({ item: d, index: i }) : void discard(d, i))}
                      >
                        Dismiss
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      {showNav && !queueEmpty && (
        <nav aria-label="Deferred queue pages" style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 12 }}>
          {showFirst && (
            <button type="button" className="button" onClick={() => go((p) => p.set('page', '1'))}>
              First page
            </button>
          )}
          {showPrev && (
            <button type="button" className="button" onClick={() => go((p) => p.set('before_id', String(page.prev_before_id)))}>
              Previous page
            </button>
          )}
          <span>
            Page {fmt(page.number)} of {fmt(page.total_pages)}
          </span>
          {showNext && (
            <button type="button" className="button" onClick={() => go((p) => p.set('after_id', String(page.next_after_id)))}>
              Next page
            </button>
          )}
          {showLast && (
            <button type="button" className="button" onClick={() => go((p) => p.set('page', 'last'))}>
              Last page
            </button>
          )}
          {page.total_pages > 1 && (
            <form onSubmit={submitJump} noValidate style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <label htmlFor="deferred-go-to-page">Go to page</label>
              <input
                id="deferred-go-to-page"
                className="form-input"
                type="number"
                inputMode="numeric"
                min={1}
                max={page.total_pages}
                value={jumpValue}
                aria-invalid={jumpError ? true : undefined}
                aria-describedby={jumpError ? 'deferred-go-to-page-error' : undefined}
                onChange={(e) => setJumpValue(e.target.value)}
                style={{ width: 96 }}
              />
              <button type="submit" className="button">
                Go
              </button>
            </form>
          )}
          {jumpError && (
            <span id="deferred-go-to-page-error" role="alert">
              {jumpError}
            </span>
          )}
        </nav>
      )}

      {/* A handed-over video waits only for the next run, so dismissing it
          means nothing will pick it up (design: "Deferred rows handed over"). */}
      <ConfirmDialog
        open={confirmDismiss !== null}
        title="Dismiss this deferred item?"
        confirmLabel="Dismiss"
        destructive
        onConfirm={() => {
          const target = confirmDismiss
          setConfirmDismiss(null)
          if (target) void discard(target.item, target.index)
        }}
        onCancel={() => setConfirmDismiss(null)}
      >
        It will not be retried, but the historical record will be preserved.
      </ConfirmDialog>
    </div>
  )
}
