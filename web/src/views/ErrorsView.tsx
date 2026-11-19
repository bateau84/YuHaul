import { Fragment, useEffect, useRef, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import * as api from '../api/client'
import { ApiError } from '../api/core'
import { ClosedNotice, RefusalAlert } from '../components/ClosedNotice'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { FailureReason } from '../components/FailureReason'
import { DeferredQueue } from '../components/DeferredQueue'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { useToast } from '../hooks/useToast'
import { categoryLabel, offersAccountStatus } from '../lib/failure-reason'
import { duplicateTitles, targetName, videoIdSuffix } from '../lib/same-title'
import {
  diagCode,
  isClosedEntry,
  ORIGINAL_DECISION_UNAVAILABLE,
  originalDecisionUnavailable,
  refusalText,
  usableEntry,
} from '../lib/final-outcome'
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
  const activeCount = errors.filter((e) => !isClosedEntry(e)).length
  const [queueTotal, setQueueTotal] = useState<number | null>(null)
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState(false)
  const [retrying, setRetrying] = useState(false)
  // Entries whose Retry was refused with video_already_final (BR-039), by id.
  // The refusal stays on screen next to the closed row until the page is left.
  const [refusals, setRefusals] = useState<Record<number, string | undefined>>({})
  const [confirmDismiss, setConfirmDismiss] = useState<ErrorEntry | null>(null)
  const retryButtonRef = useRef<HTMLButtonElement>(null)
  const firstRowRef = useRef<HTMLTableRowElement>(null)
  const headingRef = useRef<HTMLHeadingElement>(null)
  const bodyRef = useRef<HTMLTableSectionElement>(null)
  // Row index to focus once a removed row (Dismiss, Remove or a successful
  // Retry) has left the list.
  const focusAfterDismiss = useRef<number | null>(null)
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
    const index = focusAfterDismiss.current
    if (index === null) return
    focusAfterDismiss.current = null
    const row = bodyRef.current?.querySelectorAll<HTMLTableRowElement>('tr[data-error-row]')[index]
    if (row) row.focus()
    else headingRef.current?.focus()
  }, [errors])

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

  async function retryError(id: number) {
    try {
      await api.retryError(id)
      focusAfterDismiss.current = errors.findIndex((e) => e.id === id)
      setErrors((prev) => prev.filter((e) => e.id !== id))
      addToast('Error queued for retry', 'success')
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && err.code === 'video_already_final') {
        // Nothing was sent. Show the refusal and the closed row, not a toast.
        const entry = usableEntry<ErrorEntry>(err.entry, id)
        const outcome = err.finalOutcome ?? (entry ? entry.video_final_outcome : undefined)
        setRefusals((prev) => ({ ...prev, [id]: outcome }))
        if (entry) {
          setErrors((prev) => prev.map((e) => (e.id === id ? { ...e, ...entry } : e)))
        } else {
          // The refusal itself says the video is final, so close the row we have.
          setErrors((prev) => prev.map((e) => (e.id === id ? { ...e, closed_reason: 'final_outcome', video_final_outcome: outcome as ErrorEntry['video_final_outcome'] } : e)))
        }
        return
      }
      addToast(err instanceof Error ? err.message : 'Failed to retry error', 'error')
    }
  }

  async function dismissError(id: number, toast = 'Error dismissed') {
    try {
      await api.dismissError(id)
      focusAfterDismiss.current = errors.findIndex((e) => e.id === id)
      setErrors((prev) => prev.filter((e) => e.id !== id))
      addToast(toast, 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to dismiss error', 'error')
    }
  }

  if (loading) return <div className="empty-state">Loading queue...</div>

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
          {fetchError ? 'Errors' : `Errors (${activeCount})`}
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

      {fetchError ? (
        <FetchErrorRegion
          ref={retryButtonRef}
          message="Failed to load queue. Check your connection and try again."
          onRetry={handleRetry}
          retrying={retrying}
        />
      ) : (
        <>
          {tab === 'errors' && (
            <div className="card">
              {errors.length === 0 ? (
                <div className="empty-state">No errors. All processing is running smoothly.</div>
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
                      const unreadable = !closed && isUnreadableDecision(e)
                      const videoKey = e.external_video_id
                      const idText = String(e.external_video_id ?? e.video_id)
                      const suffix = videoIdSuffix(e.video_title, sharedTitles, idText)
                      const target = targetName(e.video_title, sharedTitles, idText)
                      return (
                        <Fragment key={e.id}>
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
                                <ClosedNotice outcome={e.video_final_outcome} videoId={videoKey} target={target} />
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
                                    aria-label={`Retry ${target}`}
                                    onClick={() => retryError(e.id)}
                                  >
                                    Retry
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
            </div>
          )}

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
