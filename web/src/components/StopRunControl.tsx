import { forwardRef, useId, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../api/core'
import { stopRun, stopRefusalCode, type StopRefusalCode } from '../api/runs'
import { canOfferStop, isStopping, type Run } from '../types'
import { simulationMode } from '../lib/outcome'
import { ModalDialog } from './ModalDialog'

// "Stop an Active Run" (admin UI design, BR-035). The control and its dialog
// live in StopRunControl; the persistent page message lives in StopRunMessage
// so the page can place it at the top and move focus to it once the button has
// been replaced. Branching is on HTTP status and machine code, never message
// text.

type StopTarget = Pick<Run, 'id' | 'run_type' | 'status' | 'stop_requested_at' | 'initiated_by' | 'discardable' | 'executing'>

export type StopOutcome =
  | { kind: 'accepted'; runId: number }
  | { kind: 'already_stopping'; runId: number }
  | { kind: 'not_found'; runId: number }
  | { kind: 'ended'; runId: number }
  | { kind: 'not_active'; runId: number; discardable: boolean }
  | { kind: 'shutting_down'; runId: number }
  | { kind: 'unavailable'; runId: number }
  | { kind: 'unconfirmed'; runId: number }

type DialogRefusal = Extract<StopOutcome, { kind: 'shutting_down' | 'unavailable' }>

// Refusals shown inside the open dialog; every other failure closes it.
function classify(err: unknown, run: StopTarget): StopOutcome {
  const runId = run.id
  const code: StopRefusalCode | undefined = stopRefusalCode(err)
  if (code === 'shutting_down') return { kind: 'shutting_down', runId }
  if (code === 'run_not_found') return { kind: 'not_found', runId }
  if (code === 'run_ended') return { kind: 'ended', runId }
  if (code === 'run_not_active') return { kind: 'not_active', runId, discardable: run.discardable === true }
  if (err instanceof ApiError && err.status === 503 && code !== 'stop_failed') return { kind: 'unavailable', runId }
  return { kind: 'unconfirmed', runId }
}

function runKind(run: StopTarget): string {
  const sim = simulationMode(run.run_type)
  if (sim) return run.run_type === 'dry_run' ? 'a dry run' : run.run_type === 'replay' ? 'a replay' : 'a backtest'
  if (run.initiated_by === 'queue_retry') return 'a queue retry run'
  if (run.initiated_by === 'run_now' || run.run_type === 'manual') return 'a Run Now run'
  if (run.initiated_by === 'schedule' || run.run_type === 'scheduled') return 'a scheduled run'
  return 'a production run'
}

interface StopRunControlProps {
  run: StopTarget
  /** Called with the result of a press that closed the dialog, so the page can show the message and re-read the run. */
  onOutcome: (outcome: StopOutcome) => void
}

export function StopRunControl({ run, onOutcome }: StopRunControlProps) {
  const [open, setOpen] = useState(false)
  const [pending, setPending] = useState(false)
  const [dialogRefusal, setDialogRefusal] = useState<DialogRefusal | null>(null)
  const [accepted, setAccepted] = useState(false)
  const pendingRef = useRef(false)
  const keepRef = useRef<HTMLButtonElement>(null)
  const titleId = useId()
  const descId = useId()
  const id = run.id
  const simulation = simulationMode(run.run_type) !== null

  // Shown only while the saved status says running and this server is actually
  // executing the run (canOfferStop): a stuck legacy run is saved as running
  // but nothing executes it, so it never gets Stop.
  if (!canOfferStop(run)) return null
  if (accepted || isStopping(run)) {
    return <p style={{ margin: '8px 0 0' }}>Stopping run {id}…</p>
  }

  function close() {
    if (pendingRef.current) return
    setOpen(false)
    setDialogRefusal(null)
  }

  async function confirm() {
    if (pendingRef.current) return
    pendingRef.current = true
    setPending(true)
    setDialogRefusal(null)
    try {
      const res = await stopRun(id)
      setAccepted(true)
      setOpen(false)
      onOutcome({ kind: res.stop === 'already_stopping' ? 'already_stopping' : 'accepted', runId: id })
    } catch (err) {
      const outcome = classify(err, run)
      if (outcome.kind === 'shutting_down' || outcome.kind === 'unavailable') {
        setDialogRefusal(outcome)
      } else {
        setOpen(false)
        onOutcome(outcome)
      }
    } finally {
      pendingRef.current = false
      setPending(false)
    }
  }

  return (
    <>
      <button type="button" className="button button--danger" onClick={() => setOpen(true)}>
        Stop run {id}
      </button>
      {open && (
        <ModalDialog labelledBy={titleId} describedBy={descId} initialFocusRef={keepRef} onCancel={close}>
          <div aria-busy={pending}>
            <h2 id={titleId} className="modal-title">Stop run {id}?</h2>
            <div id={descId}>
              {simulation ? (
                <>
                  <p>Run {id} is {runKind(run)} and is running now. It makes no changes to your playlists.</p>
                  <ul>
                    <li>Run {id} will stop and will never be resumed. It will be shown as “Stopped by user”.</li>
                    <li>Results saved so far stay visible and are marked incomplete.</li>
                    <li>Nothing is added to the deferred queue, and no queue item or playlist is changed.</li>
                    <li>No new request is sent to YouTube after you confirm. This cannot be undone.</li>
                  </ul>
                </>
              ) : (
                <>
                  <p>Run {id} is {runKind(run)} and is running now.</p>
                  <ul>
                    <li>Run {id} will stop and will never be resumed. It will be shown as “Stopped by user”, not as failed.</li>
                    <li>Its unfinished videos will be put in the deferred queue. The next new run, from Run Now or the schedule, will process them automatically.</li>
                    <li>No new request is sent to YouTube after you confirm. A playlist addition that was already sent and has no answer yet will be shown as unconfirmed and will not be sent again automatically.</li>
                    <li>Results saved so far are kept. This cannot be undone.</li>
                  </ul>
                  <p>Stopping can take a moment while the run saves its work.</p>
                </>
              )}
            </div>
            {dialogRefusal && (
              <div role="alert">
                {dialogRefusal.kind === 'shutting_down' ? (
                  <p>Stop did nothing. YuHaul is shutting down, so run {id} was not stopped. Nothing was changed. The shutdown will interrupt the run, and you can stop it again once it is running again.</p>
                ) : (
                  <p>Stop did nothing. YuHaul can&apos;t accept a stop right now. It may be shutting down. Nothing was changed. Try again in a minute.</p>
                )}
                <p><Link to="/runs">View runs</Link></p>
              </div>
            )}
            <div className="modal-actions">
              <button ref={keepRef} type="button" className="button" onClick={close} disabled={pending}>
                Keep run {id} running
              </button>
              <button type="button" className="button button--danger" onClick={confirm} disabled={pending}>
                {pending ? `Stopping run ${id}…` : `Stop run ${id}`}
              </button>
            </div>
          </div>
        </ModalDialog>
      )}
    </>
  )
}

interface StopRunMessageProps {
  outcome: StopOutcome
  /** Hide the "View run" link when already on that run's page. */
  onRunPage?: boolean
  onDismiss: () => void
}

// Persistent page message for a stop press. Acknowledgements are role="status"
// and take focus; refusals are role="alert" and never steal focus.
export const StopRunMessage = forwardRef<HTMLDivElement, StopRunMessageProps>(function StopRunMessage(
  { outcome, onRunPage, onDismiss },
  ref,
) {
  const id = outcome.runId
  const isStatus = outcome.kind === 'accepted' || outcome.kind === 'already_stopping'
  const viewRun = !onRunPage && <Link to={`/runs/${id}`}>View run {id}</Link>
  let body: React.ReactNode
  let link: React.ReactNode = viewRun
  switch (outcome.kind) {
    case 'accepted':
      body = <p style={{ margin: 0 }}>Stopping: run {id} is stopping and will never be resumed. It is not stopped yet. The status will change to “Stopped by user” when it has finished saving.</p>
      break
    case 'already_stopping':
      body = <p style={{ margin: 0 }}>Stopping: run {id} is already stopping.</p>
      break
    case 'not_found':
      body = <p style={{ margin: 0 }}>Stop did nothing. Run {id} was not found. Nothing was changed.</p>
      link = <Link to="/runs">View runs</Link>
      break
    case 'ended':
      body = <p style={{ margin: 0 }}>Stop did nothing. Run {id} has already ended, so there is nothing to stop. Nothing was changed.</p>
      break
    case 'not_active':
      body = (
        <p style={{ margin: 0 }}>
          Stop did nothing. Run {id} is not running, so there is nothing to stop. Nothing was changed.
          {outcome.discardable && ' It cannot be resumed. You can discard it from its run page.'}
        </p>
      )
      break
    case 'shutting_down':
    case 'unavailable':
      // Normally shown inside the dialog; kept here so the type is total.
      body = <p style={{ margin: 0 }}>Stop did nothing. Nothing was changed.</p>
      link = <Link to="/runs">View runs</Link>
      break
    default:
      body = <p style={{ margin: 0 }}>Could not confirm whether run {id} was stopped. Open the run to check its status. If it is still running, you can press Stop again; stopping twice has the same result as stopping once.</p>
  }
  return (
    <div
      ref={ref}
      tabIndex={-1}
      className="card"
      role={isStatus ? 'status' : 'alert'}
      aria-live={isStatus ? 'polite' : undefined}
      style={{ marginBottom: 16 }}
    >
      {body}
      {link && <p style={{ margin: '8px 0 0' }}>{link}</p>}
      <p style={{ margin: '8px 0 0' }}>
        <button type="button" className="button" onClick={onDismiss}>Dismiss</button>
      </p>
    </div>
  )
})
