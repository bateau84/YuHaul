import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/core'
import { discardRefusalCode, discardRun, type DiscardRefusalCode, type DiscardRunResult } from '../api/runs'
import { ModalDialog } from './ModalDialog'

// Reason sentences for the unusable-run causes (design: "Run Now — Resume After
// a Time Limit", reason table). Anything else gets the neutral fallback; the raw
// value is never shown.
const REASON_SENTENCES: Record<string, string> = {
  snapshot_unavailable: 'Its saved settings are missing.',
  snapshot_unreadable: 'Its saved settings cannot be read.',
  snapshot_inadmissible: 'Its saved settings cannot be used safely.',
  checkpoint_unrecognized: 'Its saved progress point is not recognized.',
  terminal_timestamp_contradiction:
    'Its saved record is contradictory: it has a completion time but is marked interrupted.',
}
const FALLBACK_REASON = 'Its saved data cannot be used to continue it safely.'

function discardReasonSentence(cause: string | undefined | null): string {
  return (cause && REASON_SENTENCES[cause]) || FALLBACK_REASON
}

interface Props {
  runId: number
  /** The run read's discard_cause; selects the reason sentence. */
  cause?: string | null
  /** True when rendered on this run's own page: the "View run" link is omitted. */
  onRunPage?: boolean
  /** Called after a confirmed discard so the page can re-read the run. */
  onDiscarded?: (result: DiscardRunResult) => void
  /** Called after a refusal or uncertain result so the page can re-read the run. */
  onRefused?: (code: DiscardRefusalCode | undefined) => void
}

type Failure = { retryable: boolean; body: ReactNode }

function failureFor(err: unknown, runId: number, onRunPage: boolean): Failure {
  const code = discardRefusalCode(err)
  const viewRun = onRunPage ? null : <Link to={`/runs/${runId}`}>View run {runId}</Link>
  switch (code) {
    case 'run_not_found':
      return {
        retryable: false,
        body: (
          <>
            Discard did nothing. Run {runId} was not found. Nothing was changed. <Link to="/runs">View runs</Link>
          </>
        ),
      }
    case 'run_ended':
      return {
        retryable: false,
        body: (
          <>
            Discard did nothing. Run {runId} has already ended, so it was not discarded again. Nothing was changed.{' '}
            {viewRun}
          </>
        ),
      }
    case 'run_not_discardable':
      return {
        retryable: false,
        body: (
          <>
            Discard did nothing. Run {runId} cannot be discarded now. Only the run that is blocking Run Now can be
            discarded, and run {runId} is not that run. Its state may have changed. Nothing was changed. {viewRun}{' '}
            <Link to="/">Go to Overview</Link>
          </>
        ),
      }
    case 'production_run_active': {
      const active = err instanceof ApiError ? err.runId : undefined
      return {
        retryable: true,
        body: (
          <>
            Discard did nothing. A production run is already running. Run {runId} was not discarded and nothing was
            changed. Wait for it to finish, then try again.{' '}
            {active !== undefined ? <Link to={`/runs/${active}`}>View run {active}</Link> : <Link to="/runs">View runs</Link>}
          </>
        ),
      }
    }
    case 'shutting_down':
    case 'startup_recovery_active':
      return {
        retryable: true,
        body: (
          <>
            Discard did nothing. YuHaul can&apos;t discard a run right now. It may be starting up or shutting down.
            Nothing was changed. Try again in a minute. <Link to="/runs">View runs</Link>
          </>
        ),
      }
    case 'discard_failed':
      return {
        retryable: true,
        body: (
          <>
            Discard did nothing. Run {runId} could not be saved as discarded, so it is still interrupted and nothing
            was changed. You can try again.
          </>
        ),
      }
    default:
      // discard_outcome_unknown, invalid_run_id, network failure, unknown code.
      return {
        retryable: false,
        body: (
          <>
            Could not confirm whether run {runId} was discarded. Open the run to check its status before trying
            again. {viewRun ?? 'You are on that run now; reload it to check its status.'}
          </>
        ),
      }
  }
}

/**
 * "Discard run {id}": button, confirmation dialog, pending state,
 * acknowledgement and refusal messages (design: "Run Now — Discarding a Run
 * That Cannot Be Resumed"). The component never infers discardability: the
 * parent renders it only when the run read says the run is discardable.
 */
export function DiscardRunControl({ runId, cause, onRunPage = false, onDiscarded, onRefused }: Props) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [dialogFailure, setDialogFailure] = useState<Failure | null>(null)
  const [pageFailure, setPageFailure] = useState<Failure | null>(null)
  const [done, setDone] = useState(false)
  const keepRef = useRef<HTMLButtonElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const ackRef = useRef<HTMLDivElement>(null)
  const inFlight = useRef(false)
  const titleId = useId()
  const descId = useId()
  const reason = discardReasonSentence(cause)

  useEffect(() => {
    if (done) ackRef.current?.focus({ preventScroll: true })
  }, [done])

  function close() {
    if (inFlight.current) return
    setOpen(false)
    setDialogFailure(null)
    // Some browsers do not focus a button on click, so the shell's remembered
    // element may be <body>; return focus to the trigger explicitly.
    triggerRef.current?.focus()
  }

  async function confirm() {
    if (inFlight.current) return
    inFlight.current = true
    setPending(true)
    setDialogFailure(null)
    setPageFailure(null)
    try {
      const result = await discardRun(runId)
      inFlight.current = false
      setPending(false)
      setOpen(false)
      setDone(true)
      onDiscarded?.(result)
    } catch (err) {
      inFlight.current = false
      setPending(false)
      const failure = failureFor(err, runId, onRunPage)
      if (failure.retryable) {
        setDialogFailure(failure)
      } else {
        setOpen(false)
        setPageFailure(failure)
      }
      onRefused?.(discardRefusalCode(err))
    }
  }

  if (done) {
    return (
      <div ref={ackRef} role="status" tabIndex={-1} className="discard-run-ack">
        <p>
          <strong>Discarded:</strong> run {runId} is now marked failed and will not be resumed. Its unfinished videos
          are in the deferred queue, and the next new run will process them automatically. Nothing was sent to YouTube.
          Press Run Now to continue with the next stopped run, or to start a new run.
        </p>
        <p>
          <Link to="/errors">View deferred queue</Link>
          {!onRunPage && (
            <>
              {' '}
              <Link to={`/runs/${runId}`}>View run {runId}</Link>
            </>
          )}
        </p>
      </div>
    )
  }

  return (
    <>
      {pageFailure && (
        <div role="alert" className="discard-run-alert">
          {pageFailure.body}
        </div>
      )}
      <button type="button" ref={triggerRef} className="button" onClick={() => { setPageFailure(null); setOpen(true) }}>
        Discard run {runId}
      </button>
      {open && (
        <ModalDialog labelledBy={titleId} describedBy={descId} initialFocusRef={keepRef} onCancel={close}>
          <div aria-busy={pending}>
            <h2 id={titleId} className="modal-title">Discard run {runId}?</h2>
            <div id={descId}>
              <p>
                Run {runId} stopped at its time limit and cannot be resumed. {reason}
              </p>
              <ul>
                <li>Run {runId} will be marked failed. It will never be resumed.</li>
                <li>
                  Its unfinished videos will be put in the deferred queue. The next new run, from Run Now or the
                  schedule, will process them automatically.
                </li>
                <li>
                  Nothing is sent to YouTube. Playlist additions already recorded stay recorded. An addition whose
                  outcome could not be confirmed stays marked as unconfirmed and still blocks a repeat.
                </li>
                <li>This cannot be undone.</li>
              </ul>
              <p>
                Discarding does not start a run. After this, Run Now continues the next stopped run, or starts a new
                run if none is left. The deferred videos wait until a new run starts. The schedule is not affected.
              </p>
            </div>
            {dialogFailure && (
              <div role="alert" className="discard-run-alert">
                {dialogFailure.body}
              </div>
            )}
            <div className="modal-actions">
              <button type="button" ref={keepRef} className="button" disabled={pending} onClick={close}>
                Keep run {runId}
              </button>
              <button type="button" className="button button--danger" disabled={pending} onClick={confirm}>
                {pending ? `Discarding run ${runId}…` : `Discard run ${runId}`}
              </button>
            </div>
          </div>
        </ModalDialog>
      )}
    </>
  )
}
