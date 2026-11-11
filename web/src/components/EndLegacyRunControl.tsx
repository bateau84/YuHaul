import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/core'
import { endRefusalCode, endRun, type EndRefusalCode, type EndRunResult } from '../api/runs'
import { ModalDialog } from './ModalDialog'

// "Mark a Legacy Run as Ended" (admin UI design, BR-036). The component never
// infers endability: the page renders it only when the run read says
// `endable` is true. Branching is on HTTP status and machine code, never on
// message text.

interface Props {
  runId: number
  /** The run's saved status; named in the confirmation. */
  savedStatus: 'running' | 'interrupted'
  /**
   * False once the run read says the run is no longer endable: the button is
   * withdrawn but a refusal message or acknowledgement already shown stays.
   */
  available?: boolean
  /** Called after any answer or network failure so the page re-reads the run. */
  onSettled?: (result: EndRunResult | undefined) => void
}

type Failure = { retryable: boolean; body: ReactNode }

const simulationKinds: Record<string, string> = { dry_run: 'a dry run', replay: 'a replay', backtest: 'a backtest' }

function failureFor(err: unknown, runId: number): Failure {
  const code: EndRefusalCode | undefined = endRefusalCode(err)
  const viewRun = <Link to={`/runs/${runId}`}>View run {runId}</Link>
  const prefix = 'Mark as ended did nothing.'
  if (code === 'run_not_found') {
    return { retryable: false, body: <>{prefix} Run {runId} was not found. Nothing was changed. <Link to="/runs">View runs</Link></> }
  }
  if (code === 'run_ended') {
    return { retryable: false, body: <>{prefix} Run {runId} has already ended. Nothing was changed. {viewRun}</> }
  }
  if (code === 'run_not_endable' && err instanceof ApiError) {
    if (err.reason === 'not_legacy') {
      return { retryable: false, body: <>{prefix} Run {runId} is not an older-version run, so it cannot be marked as ended. Nothing was changed. {viewRun}</> }
    }
    if (err.reason === 'not_production') {
      const kind = (err.runType && simulationKinds[err.runType]) || 'not a production run'
      return { retryable: false, body: <>{prefix} Run {runId} is {kind}, so it cannot be marked as ended. Nothing was changed. {viewRun}</> }
    }
    if (err.reason === 'executing') {
      return { retryable: false, body: <>{prefix} Run {runId} is running now, so it cannot be marked as ended. Nothing was changed. To end a running run, use “Stop run {runId}”. {viewRun}</> }
    }
  }
  if (code === 'production_run_active') {
    return { retryable: true, body: <>{prefix} A production run is running, so run {runId} was not marked as ended. Nothing was changed. Wait for it to finish or stop it, then try again. <Link to="/runs">View runs</Link></> }
  }
  if (code === 'startup_recovery_active') {
    return { retryable: true, body: <>{prefix} YuHaul is starting up and recovering runs, so run {runId} was not marked as ended. Nothing was changed. Try again in a minute.</> }
  }
  if (code === 'shutting_down') {
    return { retryable: true, body: <>{prefix} YuHaul is shutting down, so run {runId} was not marked as ended. Nothing was changed. Try again after it has started again.</> }
  }
  if (code === 'end_failed') {
    const saved = err instanceof ApiError && (err.savedStatus === 'running' || err.savedStatus === 'interrupted') ? ` (${err.savedStatus})` : ''
    return { retryable: true, body: <>{prefix} Run {runId} could not be marked as ended, so it keeps its earlier status{saved}. Nothing was changed. You can try again.</> }
  }
  if (err instanceof ApiError && err.status === 503) {
    return { retryable: true, body: <>{prefix} YuHaul can&apos;t mark a run as ended right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.</> }
  }
  // end_outcome_unknown, invalid_run_id, network failure, unknown code.
  return {
    retryable: false,
    body: <>Could not confirm whether run {runId} was marked as ended. Open the run to check its status. If it is not marked failed, you can try again; doing it twice has the same result as doing it once.</>,
  }
}

export function EndLegacyRunControl({ runId, savedStatus, available = true, onSettled }: Props) {
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

  useEffect(() => {
    if (done) ackRef.current?.focus({ preventScroll: true })
  }, [done])

  function close() {
    if (inFlight.current) return
    setOpen(false)
    setDialogFailure(null)
    // Some browsers do not focus a button on click; return focus explicitly.
    triggerRef.current?.focus()
  }

  async function confirm() {
    if (inFlight.current) return
    inFlight.current = true
    setPending(true)
    setDialogFailure(null)
    setPageFailure(null)
    try {
      const result = await endRun(runId)
      inFlight.current = false
      setPending(false)
      setOpen(false)
      setDone(true)
      onSettled?.(result)
    } catch (err) {
      inFlight.current = false
      setPending(false)
      const failure = failureFor(err, runId)
      if (failure.retryable) {
        setDialogFailure(failure)
      } else {
        setOpen(false)
        setPageFailure(failure)
      }
      onSettled?.(undefined)
    }
  }

  if (done) {
    return (
      <div ref={ackRef} role="status" tabIndex={-1} className="discard-run-ack">
        <p>
          <strong>Marked as ended:</strong> run {runId} is now marked failed. You ended it as a legacy run, and it will
          not be resumed. Its unfinished videos were not processed and were not added to any queue. Nothing was sent to
          YouTube.
        </p>
        <p>
          <Link to="/runs">View runs</Link>
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
      {available && (
        <button type="button" ref={triggerRef} className="button" onClick={() => { setPageFailure(null); setOpen(true) }}>
          Mark run {runId} as ended
        </button>
      )}
      {available && open && (
        <ModalDialog labelledBy={titleId} describedBy={descId} initialFocusRef={keepRef} onCancel={close}>
          <div aria-busy={pending}>
            <h2 id={titleId} className="modal-title">Mark run {runId} as ended?</h2>
            <div id={descId}>
              <p>
                Run {runId} was created by an older version of YuHaul. Its saved status is {savedStatus}, but nothing is
                executing it and it cannot be resumed.
              </p>
              <ul>
                <li>Run {runId} will be marked failed. It will never be resumed.</li>
                <li>
                  Its unfinished videos will not be processed. Unlike stopping or discarding a run, this does not add
                  them to the deferred queue or the Error Queue.
                </li>
                <li>
                  Nothing else changes. Its saved results, playlist records, send guards and queue items stay as they
                  are. A playlist addition whose outcome could not be confirmed stays marked as unconfirmed. Nothing is
                  sent to YouTube.
                </li>
                <li>This cannot be undone.</li>
              </ul>
              <p>This does not start or resume any run. The schedule is not affected.</p>
            </div>
            {dialogFailure && (
              <div role="alert" className="discard-run-alert">
                {dialogFailure.body}
              </div>
            )}
            <div className="modal-actions">
              <button type="button" ref={keepRef} className="button" disabled={pending} onClick={close}>
                Keep run {runId} as it is
              </button>
              <button type="button" className="button button--danger" disabled={pending} onClick={confirm}>
                {pending ? `Marking run ${runId} as ended…` : `Mark run ${runId} as ended`}
              </button>
            </div>
          </div>
        </ModalDialog>
      )}
    </>
  )
}
