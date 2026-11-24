import { Fragment, useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import * as api from '../api/client'
import { ApiError } from '../api/core'
import { ClosedNotice, RefusalAlert } from '../components/ClosedNotice'
import { RemovalAlert, RemovedNotice } from '../components/RemovedNotice'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { FailureReason } from '../components/FailureReason'
import { DeferredQueue } from '../components/DeferredQueue'
import { ChannelScanErrors } from '../components/ChannelScanErrors'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { useToast } from '../hooks/useToast'
import { categoryLabel, offersAccountStatus } from '../lib/failure-reason'
import { useLinkFocusOnce } from '../lib/focus'
import { duplicateTitles, targetName, videoIdSuffix } from '../lib/same-title'
import {
  diagCode,
  isClosedEntry,
  ORIGINAL_DECISION_UNAVAILABLE,
  originalDecisionUnavailable,
  refusalText,
  usableEntry,
} from '../lib/final-outcome'
import { RETRY_WORK_REMOVED, retryRunOf, usableRemovedEntry } from '../lib/retry-removed'
import type { RemovalNoticeData } from '../lib/retry-removed'
import type { ErrorEntry } from '../types'

const isUnreadableDecision = (e: ErrorEntry) => diagCode(e.error_message) === ORIGINAL_DECISION_UNAVAILABLE

export function ErrorsView() {
  // "View deferred queue" links open the Deferred tab with ?tab=deferred.
  // The tab, the Deferred page position and its reason filter live in the URL
  // (design: "The paged Deferred tab"), so reload, Back and shared links restore them.
  const [searchParams, setSearchParams] = useSearchParams()
  const tab: 'errors' | 'deferred' = searchParams.get('tab') === 'deferred' ? 'deferred' : 'errors'
  const setTab = (next: 'errors' | 'deferred') => setSearchParams(next === 'deferred' ? { tab: 'deferred' } : {})
  const [errors, setErrors] = useState<ErrorEntry[]>([])
  // A closed entry is no longer active: it is never counted (BR-039).
  // Rows that share a title name their video ID, visibly and in control names.
  const sharedTitles = duplicateTitles(errors)
  // A removed entry (the person's own removal won a running Retry) is not active either.
  const isRemoved = (e: ErrorEntry) => e.status === 'dismissed'
  const activeCount = errors.filter((e) => !isClosedEntry(e) && !isRemoved(e)).length
  const [queueTotal, setQueueTotal] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState(false)
  const [retrying, setRetrying] = useState(false)
  // Entries whose Retry was refused with video_already_final (BR-039), by id.
  // The refusal stays on screen next to the closed row until the page is left.
  const [refusals, setRefusals] = useState<Record<number, string | undefined>>({})
  // Rows whose Retry is running. The control reads "Retrying…" and is
  // aria-disabled, never disabled, so it keeps focus; Remove stays usable.
  const [retryingIds, setRetryingIds] = useState<ReadonlySet<number>>(new Set())
  // Rows still on screen whose Retry ended with 409 retry_work_removed: the
  // notice sits in the row's place and the row becomes a removed row.
  const [removals, setRemovals] = useState<Record<number, { runId?: number }>>({})
  // The same notice for an item whose row is already gone, at the top of the list.
  const [topNotices, setTopNotices] = useState<RemovalNoticeData[]>([])
  const linkFocus = useLinkFocusOnce()
  const errorsRef = useRef<ErrorEntry[]>([])
  errorsRef.current = errors
  const [confirmDismiss, setConfirmDismiss] = useState<ErrorEntry | null>(null)
  const retryButtonRef = useRef<HTMLButtonElement>(null)
  const firstRowRef = useRef<HTMLTableRowElement>(null)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const bodyRef = useRef<HTMLTableSectionElement>(null)
  // Row index to focus once a removed row (Dismiss, Remove or a successful
  // Retry) has left the list.
  // It is state, set in the same update that removes the row, so only the commit
  // that removed the row can act on it. A ref read by an effect on `errors` was
  // consumed by a still-pending effect from an earlier commit, against the old list.
  const [focusAfterDismiss, setFocusAfterDismiss] = useState<{ index: number } | null>(null)
  const recoveredRef = useRef(false)
  const { addToast } = useToast()

  useEffect(() => {
    let cancelled = false
    api.listErrors()
      .then((e) => {
        if (cancelled) return
        setErrors(e)
        setFetchError(false)
        setLoading(false)
      })
      .catch(() => {
        if (cancelled) return
        setFetchError(true)
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Focus the Retry button whenever the view settles into the fetch-error
  // state: on first entry and again after a failed retry re-enables it.
  useEffect(() => {
    if (fetchError && !retrying) retryButtonRef.current?.focus()
  }, [fetchError, retrying])

  // After a successful retry, move focus to the first row of the active tab.
  useEffect(() => {
    if (!recoveredRef.current || loading || fetchError) return
    recoveredRef.current = false
    firstRowRef.current?.focus()
  })

  // After a row is removed focus would fall to <body>: move it to the
  // row that took its place, or to the page heading when no row is left.
  useEffect(() => {
    if (focusAfterDismiss === null) return
    const row = bodyRef.current?.querySelectorAll<HTMLTableRowElement>('tr[data-error-row]')[focusAfterDismiss.index]
    if (row) row.focus()
    else headingRef.current?.focus()
  }, [focusAfterDismiss])

  async function handleRetry() {
    setRetrying(true)
    try {
      setErrors(await api.listErrors())
      setFetchError(false)
      recoveredRef.current = true
    } catch {
      addToast('Retry failed. Please try again.', 'error')
    } finally {
      setRetrying(false)
    }
  }

  async function retryError(id: number, target: string) {
    if (retryingIds.has(id)) return // aria-disabled: a second press does nothing
    setRetryingIds((prev) => new Set(prev).add(id))
    try {
      await api.retryError(id)
      setFocusAfterDismiss({ index: errors.findIndex((e) => e.id === id) })
      setErrors((prev) => prev.filter((e) => e.id !== id))
      addToast('Error queued for retry', 'success')
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.code === RETRY_WORK_REMOVED) {
        // The person's own removal won. No toast of either kind; the retry may
        // already have sent additions, so no text says nothing was sent.
        const entry = usableRemovedEntry<ErrorEntry>(err.entry, id, 'dismissed')
        const runId = retryRunOf(err.entry)
        const onScreen = errorsRef.current.some((e) => e.id === id)
        if (onScreen && entry) {
          // Focus goes to the removed row's View video link, or the heading when it has none.
          if ({ ...errorsRef.current.find((e) => e.id === id), ...entry }.external_video_id) linkFocus.arm(id)
          else headingRef.current?.focus()
          setRemovals((prev) => ({ ...prev, [id]: { runId } }))
          setErrors((prev) => prev.map((e) => (e.id === id ? { ...e, ...entry } : e)))
        } else if (onScreen) {
          // No usable entry: take the row out and keep the notice. The Retry
          // that held focus is gone and there is no row link, so focus goes to
          // the page heading.
          setErrors((prev) => prev.filter((e) => e.id !== id))
          setTopNotices((prev) => [...prev.filter((n) => n.id !== id), { id, target, runId }])
          headingRef.current?.focus()
        } else {
          // The row is already gone (dismissed in this tab): no second toast, no focus move.
          setTopNotices((prev) => [...prev.filter((n) => n.id !== id), { id, target, runId }])
        }
        return
      }
      if (err instanceof ApiError && err.status === 409 && err.code === 'video_already_final') {
        // Nothing was sent. Show the refusal and the closed row, not a toast.
        const entry = usableEntry<ErrorEntry>(err.entry, id)
        const outcome = err.finalOutcome ?? (entry ? entry.video_final_outcome : undefined)
        setRefusals((prev) => ({ ...prev, [id]: outcome }))
        // Focus goes to the closed row's View video link; a row with no video
        // link has none to take it, so the heading does.
        if ({ ...errorsRef.current.find((e) => e.id === id), ...entry }.external_video_id) linkFocus.arm(id)
        else headingRef.current?.focus()
        if (entry) {
          setErrors((prev) => prev.map((e) => (e.id === id ? { ...e, ...entry } : e)))
        } else {
          // The refusal itself says the video is final, so close the row we have.
          setErrors((prev) => prev.map((e) => (e.id === id ? { ...e, closed_reason: 'final_outcome', video_final_outcome: outcome as ErrorEntry['video_final_outcome'] } : e)))
        }
        return
      }
      addToast(err instanceof Error ? err.message : 'Failed to retry error', 'error')
    } finally {
      setRetryingIds((prev) => {
        const next = new Set(prev)
        next.delete(id)
        return next
      })
    }
  }

  async function dismissError(id: number, toast = 'Error dismissed') {
    try {
      await api.dismissError(id)
      setFocusAfterDismiss({ index: errors.findIndex((e) => e.id === id) })
      setErrors((prev) => prev.filter((e) => e.id !== id))
      addToast(toast, 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to dismiss error', 'error')
    }
  }

  if (loading && tab === 'deferred') return <div className="empty-state">Loading queue...</div>

  const fetchErrorRegion = (
    <FetchErrorRegion
      ref={retryButtonRef}
      message="Failed to load queue. Check your connection and try again."
      onRetry={handleRetry}
      retrying={retrying}
    />
  )

  return (
    <>
      <div className="page-header">
        <h1 className="page-title" ref={headingRef} tabIndex={-1}>Errors & Deferred</h1>
      </div>
      {/* Tab controls stay visible but disabled while the fetch has failed. */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }} role="tablist">
        <button
          className={`button ${tab === 'errors' ? 'button--primary' : ''}`}
          onClick={() => setTab('errors')}
          role="tab"
          aria-selected={tab === 'errors'}
          disabled={fetchError}
        >
          {fetchError || loading ? 'Errors' : `Errors (${activeCount})`}
        </button>
        <button
          className={`button ${tab === 'deferred' ? 'button--primary' : ''}`}
          onClick={() => setTab('deferred')}
          role="tab"
          aria-selected={tab === 'deferred'}
          disabled={fetchError}
        >
          {fetchError || queueTotal === null ? 'Deferred' : `Deferred (${queueTotal.toLocaleString()})`}
        </button>
      </div>

      {tab === 'errors' && (
        <>
          <section className="card" aria-labelledby="video-errors-heading">
            <h2 id="video-errors-heading" className="section-title">Video errors</h2>
            {topNotices.map((n) => (
              <RemovalAlert key={n.id} target={n.target} runId={n.runId} />
            ))}
            {loading ? (
              <div className="empty-state" role="status">Loading queue...</div>
            ) : fetchError ? (
              fetchErrorRegion
            ) : errors.length === 0 ? (
              <div className="empty-state">No video errors in the queue.</div>
            ) : (
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Video</th>
                      <th scope="col">Category</th>
                      <th scope="col">Reason</th>
                      <th scope="col">Actions</th>
                    </tr>
                  </thead>
                  <tbody ref={bodyRef}>
                    {errors.map((e, i) => {
                      const closed = isClosedEntry(e)
                      const removed = !closed && isRemoved(e)
                      const pending = retryingIds.has(e.id)
                      const unreadable = !closed && isUnreadableDecision(e)
                      const videoKey = e.external_video_id
                      const idText = String(e.external_video_id ?? e.video_id)
                      const suffix = videoIdSuffix(e.video_title, sharedTitles, idText)
                      const target = targetName(e.video_title, sharedTitles, idText)
                      return (
                        <Fragment key={e.id}>
                          {e.id in removals && (
                            <tr>
                              <td colSpan={4}>
                                <RemovalAlert runId={removals[e.id].runId} />
                              </td>
                            </tr>
                          )}
                          {e.id in refusals && (
                            <tr>
                              <td colSpan={4}>
                                <RefusalAlert text={refusalText(refusals[e.id] ?? e.video_final_outcome)} />
                              </td>
                            </tr>
                          )}
                          <tr ref={i === 0 ? firstRowRef : undefined} tabIndex={-1} data-error-row>
                            <td data-label="Video" className="cell-video">
                              {e.video_title ?? e.external_video_id ?? e.video_id}
                              {suffix && <div style={{ color: 'var(--color-text-muted)' }}>Video ID: {idText}</div>}
                            </td>
                            <td data-label="Category">{unreadable ? originalDecisionUnavailable.type : categoryLabel(e.error_category)}</td>
                            <td data-label="Reason">
                              {unreadable ? (
                                originalDecisionUnavailable.detail
                              ) : e.error_message ? (
                                <>
                                  <FailureReason message={e.error_message} subject={target} />
                                  {offersAccountStatus(e.error_message) && (
                                    <div>
                                      <Link to="/settings" aria-label={suffix ? `View account status${suffix}` : undefined}>
                                        View account status
                                      </Link>
                                    </div>
                                  )}
                                </>
                              ) : (
                                e.error_message_unavailable ? 'Reason unavailable' : 'No reason provided'
                              )}
                            </td>
                            <td data-label="Actions">
                              {closed ? (
                                <ClosedNotice outcome={e.video_final_outcome} videoId={videoKey} target={target} linkRef={linkFocus.refFor(e.id)} />
                              ) : removed ? (
                                <RemovedNotice videoId={videoKey} target={target} linkRef={linkFocus.refFor(e.id)} />
                              ) : unreadable ? (
                                <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
                                  <button
                                    className="button"
                                    aria-label={`Dismiss error: ${target}`}
                                    onClick={() => setConfirmDismiss(e)}
                                  >
                                    Dismiss
                                  </button>
                                  {videoKey && (
                                    <Link to={`/videos/${encodeURIComponent(videoKey)}`} aria-label={`View video: ${target}`}>
                                      View video
                                    </Link>
                                  )}
                                  <span>{originalDecisionUnavailable.noRetry}</span>
                                </div>
                              ) : (
                                <div style={{ display: 'flex', gap: 8 }}>
                                  <button
                                    className="button"
                                    aria-label={pending ? `Retrying ${target}` : `Retry ${target}`}
                                    aria-disabled={pending ? true : undefined}
                                    onClick={() => void retryError(e.id, target)}
                                  >
                                    {pending ? 'Retrying…' : 'Retry'}
                                  </button>
                                  <button className="button" aria-label={`Remove error: ${target}`} onClick={() => dismissError(e.id)}>
                                    Remove
                                  </button>
                                </div>
                              )}
                            </td>
                          </tr>
                        </Fragment>
                      )
                    })}
                  </tbody>
                </table>
            )}
          </section>
          <ChannelScanErrors />
        </>
      )}
      {tab === 'deferred' && fetchError && fetchErrorRegion}

      {!loading && !fetchError && (
        <>
          <ConfirmDialog
            open={confirmDismiss !== null}
            title={originalDecisionUnavailable.dismissTitle}
            confirmLabel="Dismiss"
            destructive
            onConfirm={() => {
              const target = confirmDismiss
              setConfirmDismiss(null)
              if (target) void dismissError(target.id, originalDecisionUnavailable.dismissedToast)
            }}
            onCancel={() => setConfirmDismiss(null)}
          >
            {originalDecisionUnavailable.dismissBody}
          </ConfirmDialog>

          <DeferredQueue active={tab === 'deferred'} onQueueTotal={setQueueTotal} />
        </>
      )}
    </>
  )
}
