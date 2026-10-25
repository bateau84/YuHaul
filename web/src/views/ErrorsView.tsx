import { useEffect, useRef, useState } from 'react'
import * as api from '../api/client'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { useToast } from '../hooks/useToast'
import type { ErrorEntry, DeferredItem } from '../types'

export function ErrorsView() {
  const [tab, setTab] = useState<'errors' | 'deferred'>('errors')
  const [errors, setErrors] = useState<ErrorEntry[]>([])
  const [deferred, setDeferred] = useState<DeferredItem[]>([])
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const retryButtonRef = useRef<HTMLButtonElement>(null)
  const firstRowRef = useRef<HTMLTableRowElement>(null)
  const recoveredRef = useRef(false)
  const { addToast } = useToast()

  useEffect(() => {
    let cancelled = false
    Promise.all([api.listErrors(), api.listDeferred()])
      .then(([e, d]) => {
        if (cancelled) return
        setErrors(e)
        setDeferred(d)
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

  async function handleRetry() {
    setRetrying(true)
    try {
      const [e, d] = await Promise.all([api.listErrors(), api.listDeferred()])
      setErrors(e)
      setDeferred(d)
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
      setErrors((prev) => prev.filter((e) => e.id !== id))
      addToast('Error queued for retry', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to retry error', 'error')
    }
  }

  async function dismissError(id: number) {
    try {
      await api.dismissError(id)
      setErrors((prev) => prev.filter((e) => e.id !== id))
      addToast('Error dismissed', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to dismiss error', 'error')
    }
  }

  async function retryDeferred(id: number) {
    try {
      await api.retryDeferred(id)
      setDeferred((prev) => prev.filter((d) => d.id !== id))
      addToast('Deferred item queued for retry', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to retry deferred item', 'error')
    }
  }

  async function discardDeferred(id: number) {
    try {
      await api.discardDeferred(id)
      setDeferred((prev) => prev.filter((d) => d.id !== id))
      addToast('Deferred item discarded', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to discard deferred item', 'error')
    }
  }

  if (loading) return <div className="empty-state">Loading queue...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Errors & Deferred</h1>
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
          {fetchError ? 'Errors' : `Errors (${errors.length})`}
        </button>
        <button
          className={`button ${tab === 'deferred' ? 'button--primary' : ''}`}
          onClick={() => setTab('deferred')}
          role="tab"
          aria-selected={tab === 'deferred'}
          disabled={fetchError}
        >
          {fetchError ? 'Deferred' : `Deferred (${deferred.length})`}
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
                  <tbody>
                    {errors.map((e, i) => (
                      <tr key={e.id} ref={i === 0 ? firstRowRef : undefined} tabIndex={i === 0 ? -1 : undefined}>
                        <td data-label="Video">{e.video_title ?? e.video_id}</td>
                        <td data-label="Category">{e.error_category}</td>
                        <td data-label="Reason">{e.error_message}</td>
                        <td data-label="Actions">
                          <div style={{ display: 'flex', gap: 8 }}>
                            <button className="button" onClick={() => retryError(e.id)}>
                              Retry
                            </button>
                            <button className="button" onClick={() => dismissError(e.id)}>
                              Remove
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}

          {tab === 'deferred' && (
            <div className="card">
              {deferred.length === 0 ? (
                <div className="empty-state">No deferred work. All pending items have been processed.</div>
              ) : (
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Video</th>
                      <th scope="col">Reason</th>
                      <th scope="col">Retry after</th>
                      <th scope="col">Actions</th>
                    </tr>
                  </thead>
                  <tbody>
                    {deferred.map((d, i) => (
                      <tr key={d.id} ref={i === 0 ? firstRowRef : undefined} tabIndex={i === 0 ? -1 : undefined}>
                        <td data-label="Video">{d.video_title ?? d.video_id}</td>
                        <td data-label="Reason">{d.deferral_reason}</td>
                        <td data-label="Retry after">
                          {d.retry_after ? new Date(d.retry_after).toLocaleString() : 'Next run'}
                        </td>
                        <td data-label="Actions">
                          <div style={{ display: 'flex', gap: 8 }}>
                            <button className="button" onClick={() => retryDeferred(d.id)}>
                              Retry now
                            </button>
                            <button className="button" onClick={() => discardDeferred(d.id)}>
                              Discard
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}
        </>
      )}
    </>
  )
}
