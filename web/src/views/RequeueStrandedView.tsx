import { useCallback, useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { Loader2 } from 'lucide-react'
import * as api from '../api/client'
import { ApiError } from '../api/core'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { ModalDialog } from '../components/ModalDialog'
import { BlockedVideos, StrandedCounts } from '../components/StrandedReport'
import { focusOnAttachWithoutScroll } from '../lib/focus'
import { fmt, normalizeReport, videosText } from '../lib/stranded'
import type { RequeueResult, StrandedPreview } from '../types'

// "Re-queue Stranded Videos" (admin UI design, BR-037; ARS-005 §8 and §9). A
// page, not a dialog: it holds a table and a list. Opening it, counting and
// refreshing change nothing and make no YouTube call. Confirming sends the
// preview's count as expected_requeue. The page branches on HTTP status and
// machine code, never on message text.

const DEFERRED_URL = '/errors?tab=deferred'

// ---- Copy -------------------------------------------------------------------

const PREFIX = 'Re-queue did nothing.'
const refusalSentence = {
  production_run_active: 'A production run is running, so no videos were re-queued. Nothing was changed. Wait for it to finish, then try again.',
  startup_recovery_active: 'YuHaul is starting up and recovering runs, so no videos were re-queued. Nothing was changed. Try again in a minute.',
  shutting_down: 'YuHaul is shutting down, so no videos were re-queued. Nothing was changed. Try again after it has started again.',
  unspecified: "YuHaul can't re-queue videos right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.",
  not_saved: 'The videos could not be saved to the deferred queue, so none was re-queued. Nothing was changed. You can try again.',
} as const

type RefusalKind = keyof typeof refusalSentence

function refusalBody(kind: RefusalKind): ReactNode {
  return (
    <>
      {PREFIX} {refusalSentence[kind]}
      {kind === 'production_run_active' && (
        <>
          {' '}
          <Link to="/runs">View runs</Link>
        </>
      )}
    </>
  )
}

// What the preview read says about availability, in the refusal's own words.
function unavailableBody(reason: StrandedPreview['unavailable_reason']): ReactNode {
  if (reason === 'production_run_active' || reason === 'startup_recovery_active' || reason === 'shutting_down') {
    return refusalBody(reason)
  }
  return refusalBody('unspecified')
}

interface Refusal {
  /** The dialog stays open with the message inside it; otherwise it closes. */
  stay: boolean
  body: ReactNode
}

function refusalFor(err: unknown): Refusal {
  if (err instanceof ApiError) {
    if (err.status === 409 && err.code === 'production_run_active') return { stay: true, body: refusalBody('production_run_active') }
    if (err.status === 503) {
      if (err.code === 'startup_recovery_active') return { stay: true, body: refusalBody('startup_recovery_active') }
      if (err.code === 'shutting_down') return { stay: true, body: refusalBody('shutting_down') }
      return { stay: true, body: refusalBody('unspecified') }
    }
    if (err.status === 500 && err.code === 'requeue_failed') return { stay: true, body: refusalBody('not_saved') }
  }
  // requeue_outcome_unknown, invalid_body, a network failure or any other
  // answer: the person must check the queue. No automatic retry.
  return {
    stay: false,
    body: (
      <>
        Could not confirm whether the videos were re-queued. Open the deferred queue to check. If no items show “Re-queued by user”, you
        can try again; repeating it is safe. <Link to={DEFERRED_URL}>View deferred queue</Link>
      </>
    ),
  }
}

function acknowledgement(res: RequeueResult, previewed: number): string {
  if (res.outcome === 'nothing_to_requeue') {
    return 'Nothing to re-queue. The stranded videos in the preview are no longer stranded. Nothing was changed.'
  }
  if (res.differs_from_preview) {
    return `Re-queued ${videosText(res.requeued)}. The preview showed ${fmt(previewed)}, because the videos changed between the preview and your confirmation. Each is now in the deferred queue with the reason “Re-queued by user”. Nothing was sent to YouTube and no run was started.`
  }
  return `Re-queued ${videosText(res.requeued)}. Each is now in the deferred queue with the reason “Re-queued by user”, eligible for the next run. The next new run will process them as far as quota allows. Nothing was sent to YouTube and no run was started.`
}

// ---- Page -------------------------------------------------------------------

type Phase = 'loading' | 'error' | 'ready' | 'done'

export function RequeueStrandedView() {
  const [phase, setPhase] = useState<Phase>('loading')
  const [preview, setPreview] = useState<StrandedPreview | null>(null)
  const [readAt, setReadAt] = useState<Date | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [refreshFailed, setRefreshFailed] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const [announcement, setAnnouncement] = useState('')
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [dialogFailure, setDialogFailure] = useState<ReactNode>(null)
  const [pageFailure, setPageFailure] = useState<ReactNode>(null)
  const [done, setDone] = useState<{ result: RequeueResult; previewed: number } | null>(null)

  // A read that is not the latest one is ignored when it settles.
  const seq = useRef(0)
  const inFlight = useRef(false)
  const h1Ref = useRef<HTMLHeadingElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const confirmRef = useRef<HTMLButtonElement>(null)
  const titleRef = useRef<HTMLHeadingElement>(null)
  // Set when a refusal keeps the dialog open: the confirm button is re-enabled in
  // the commit that clears `pending`, and focus is handed back to it there.
  const refocusConfirm = useRef(false)
  const titleId = useId()
  const descId = useId()
  const unavailableId = useId()

  useEffect(() => {
    document.title = 'Re-queue stranded videos — YuHaul'
  }, [])

  // Callers that want "Counting videos…" set the phase themselves; a 'refresh'
  // keeps the counts on screen while it reads.
  const load = useCallback(async (kind: 'read' | 'refresh') => {
    const mine = ++seq.current
    if (kind === 'refresh') {
      setRefreshing(true)
      setRefreshFailed(false)
    }
    try {
      const p = normalizeReport(await api.getStrandedPreview())
      if (mine !== seq.current) return
      setPreview(p)
      setReadAt(new Date())
      setPhase('ready')
      setAnnouncement(p.requeue === 0 ? 'Preview ready: nothing to re-queue.' : `Preview ready: ${videosText(p.requeue)} will be re-queued.`)
    } catch {
      if (mine !== seq.current) return
      if (kind === 'refresh') setRefreshFailed(true)
      else setPhase('error')
    } finally {
      if (mine === seq.current) {
        setRefreshing(false)
        setRetrying(false)
      }
    }
  }, [])

  useEffect(() => {
    void load('read')
    const reads = seq
    return () => {
      reads.current++
    }
  }, [load])

  // A browser drops focus to <body> when the focused button becomes disabled, and
  // the modal's Tab trap only runs for key events that start inside the dialog. So
  // while the request is pending focus waits on the dialog title (see confirm), and
  // when a refusal re-enables the buttons it returns to the one that was pressed.
  useLayoutEffect(() => {
    if (pending || !refocusConfirm.current) return
    refocusConfirm.current = false
    confirmRef.current?.focus()
  }, [pending])

  function closeDialog() {
    if (inFlight.current) return
    setOpen(false)
    setDialogFailure(null)
    // Some browsers do not focus a button on click; return focus explicitly.
    triggerRef.current?.focus()
  }

  async function confirm() {
    if (inFlight.current || !preview) return
    inFlight.current = true
    const previewed = preview.requeue
    // Move focus off the buttons before they are disabled: it stays inside the dialog.
    titleRef.current?.focus()
    setPending(true)
    setDialogFailure(null)
    setPageFailure(null)
    try {
      const result = normalizeReport(await api.requeueStranded(previewed))
      inFlight.current = false
      setPending(false)
      setOpen(false)
      setDone({ result, previewed })
      setPhase('done')
    } catch (err) {
      inFlight.current = false
      setPending(false)
      const refusal = refusalFor(err)
      if (refusal.stay) {
        refocusConfirm.current = true
        setDialogFailure(refusal.body)
      } else {
        setOpen(false)
        setPageFailure(refusal.body)
      }
    }
  }

  function previewAgain() {
    setDone(null)
    setPageFailure(null)
    h1Ref.current?.focus()
    setPhase('loading')
    void load('read')
  }

  let body: ReactNode
  if (phase === 'loading') {
    body = <p>Counting videos…</p>
  } else if (phase === 'error') {
    body = (
      <FetchErrorRegion
        message="Could not count the stranded videos. Nothing was changed."
        retrying={retrying}
        onRetry={() => {
          setRetrying(true)
          void load('read')
        }}
      />
    )
  } else if (phase === 'done' && done) {
    const { result, previewed } = done
    body = (
      <>
        <div ref={focusOnAttachWithoutScroll} role="status" tabIndex={-1} className="card">
          <p style={{ marginTop: 0 }}>{acknowledgement(result, previewed)}</p>
          <p style={{ marginBottom: 0, display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
            <Link to={DEFERRED_URL}>View deferred queue</Link>
            <button type="button" className="button" onClick={previewAgain}>
              Preview again
            </button>
          </p>
        </div>
        <StrandedCounts report={result} caption="Result counts" firstRow={`Re-queued: ${fmt(result.requeued)}`} />
        <BlockedVideos blocked={result.blocked} />
      </>
    )
  } else if (preview) {
    const n = preview.requeue
    const available = preview.available
    body = (
      <>
        <p>
          A stranded video was found by YuHaul, has no final outcome, is in no queue, and every run that touched it has ended.
          Re-queuing adds each one to the deferred queue so the next run processes it. Nothing is sent to YouTube now and no run is
          started.
        </p>
        {pageFailure && (
          <div role="alert" className="form-error">
            {pageFailure}
          </div>
        )}
        {n === 0 ? (
          <p style={{ fontWeight: 600 }}>Nothing to re-queue. There are no stranded videos right now. Nothing was changed.</p>
        ) : (
          <>
            <p style={{ fontSize: '1.125rem', fontWeight: 600 }}>
              {n === 1 ? '1 video will be re-queued.' : `${fmt(n)} videos will be re-queued.`}
            </p>
            {!available && <p id={unavailableId}>{unavailableBody(preview.unavailable_reason)}</p>}
            <p>
              The next new run, from Run Now or the schedule, will use as much of each day&apos;s quota as these videos need. Newly
              uploaded videos may wait. This can take more than one day.
            </p>
          </>
        )}
        <p style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap' }}>
          <span>Counts read at {readAt?.toLocaleTimeString()}. They are checked again when you confirm.</span>
          {/* aria-disabled, not disabled: a disabled button drops keyboard focus to <body>. */}
          <button
            type="button"
            className="button"
            aria-disabled={refreshing ? true : undefined}
            onClick={() => {
              if (!refreshing) void load('refresh')
            }}
          >
            {refreshing && <Loader2 size={16} className="spinner" aria-hidden="true" />}
            {refreshing ? 'Counting videos…' : 'Refresh counts'}
          </button>
        </p>
        {refreshFailed && (
          <div role="alert" className="form-error">
            Could not count the stranded videos again. Nothing was changed. The counts shown were read at {readAt?.toLocaleTimeString()}.
          </div>
        )}
        {n > 0 && (
          <p>
            <button
              type="button"
              ref={triggerRef}
              className="button button--primary"
              aria-disabled={!available ? true : undefined}
              aria-describedby={!available ? unavailableId : undefined}
              onClick={() => {
                if (!available) return
                setPageFailure(null)
                setOpen(true)
              }}
            >
              Re-queue {videosText(n)}
            </button>
          </p>
        )}
        <StrandedCounts report={preview} caption="Preview counts" firstRow={`Will be re-queued: ${fmt(n)}`} />
        <BlockedVideos blocked={preview.blocked} />
        {open && n > 0 && (
          <ModalDialog labelledBy={titleId} describedBy={descId} initialFocusRef={cancelRef} onCancel={closeDialog}>
            <div
              aria-busy={pending}
              onKeyDown={(e) => {
                // Both buttons are disabled while pending, so nothing in the dialog is
                // tabbable: keep focus where it is rather than let Tab leave the modal.
                if (pending && e.key === 'Tab') e.preventDefault()
              }}
            >
              <h2 id={titleId} ref={titleRef} tabIndex={-1} className="modal-title">
                Re-queue {videosText(n)}?
              </h2>
              <div id={descId}>
                <p>
                  YuHaul will add {videosText(n)} to the deferred queue. Each will be eligible for the next run at once.
                </p>
                <ul>
                  <li>
                    The next new run, from Run Now or the schedule, will process them as far as each day&apos;s quota allows. It may use
                    the whole day&apos;s quota, so newly uploaded videos may wait, and this can take more than one day.
                  </li>
                  <li>Nothing is sent to YouTube now, and no run is started.</li>
                  <li>
                    Blocked videos, videos held by a run that has not ended, videos you removed, and videos already in a queue are not
                    re-queued.
                  </li>
                  <li>The counts are checked again when you confirm. If they have changed, you will be told.</li>
                  <li>There is no undo for the whole batch. Each item can be dismissed on its own from the Deferred tab.</li>
                </ul>
              </div>
              {dialogFailure && (
                <div role="alert" className="form-error">
                  {dialogFailure}
                </div>
              )}
              <div className="modal-actions">
                <button type="button" ref={cancelRef} className="button" disabled={pending} onClick={closeDialog}>
                  Cancel
                </button>
                <button type="button" ref={confirmRef} className="button button--primary" disabled={pending} onClick={() => void confirm()}>
                  {pending ? 'Re-queuing…' : `Re-queue ${videosText(n)}`}
                </button>
              </div>
            </div>
          </ModalDialog>
        )}
      </>
    )
  }

  return (
    <>
      <p>
        <Link to={DEFERRED_URL}>Back to Deferred</Link>
      </p>
      <div className="page-header">
        <h1 className="page-title" ref={h1Ref} tabIndex={-1}>
          Re-queue stranded videos
        </h1>
      </div>
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>
      {body}
    </>
  )
}
