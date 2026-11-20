import type { Run } from '../types'

// Who ended a failed run, from the machine reason the server vets
// (run-initiation ARS-004 §1, ARS-002 §1). The UI branches on the operation in
// the closed diag reason, never on message text. Only a failed run can have
// been ended this way.
export type RunEnding = 'ended_by_user' | 'discarded_by_user'

const OPERATION = /^diag:v1 phase=\S+ operation=(end_legacy_run|discard_run) code=\S+$/

export function runEnding(run: Pick<Run, 'status' | 'interrupt_reason'>): RunEnding | null {
  if (run.status !== 'failed' || !run.interrupt_reason) return null
  const op = OPERATION.exec(run.interrupt_reason)?.[1]
  if (op === 'end_legacy_run') return 'ended_by_user'
  if (op === 'discard_run') return 'discarded_by_user'
  return null
}
