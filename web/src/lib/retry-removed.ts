// Copy and shape checks for the 409 retry_work_removed answer to Retry (admin UI
// design 3a). The UI branches on the machine code, never on message text. The
// copy never says that nothing was sent: the retry may have sent additions
// before the person's removal won.

export const RETRY_WORK_REMOVED = 'retry_work_removed'

export const retryRemovedNotice =
  'This item was removed while its retry was running. The retry may already have sent playlist additions to YouTube. It will not be retried again.'

export const removedRowText = 'Removed from a queue by you. It will not be retried.'

// First line of the Deferred Dismiss confirmation, only while that row's retry is pending.
export const pendingRetryDismissLine =
  'A retry of this item is running now. Dismissing removes the item, but the retry may already have sent playlist additions to YouTube.'

// The entry in the 409 body, trusted only when it is the same item in the
// removed state of its queue ('dismissed' for Errors, 'discarded' for Deferred).
export function usableRemovedEntry<T extends { id: number }>(entry: unknown, id: number, removedStatus: 'dismissed' | 'discarded'): T | null {
  if (typeof entry !== 'object' || entry === null) return null
  const e = entry as { id?: unknown; status?: unknown }
  return e.id === id && e.status === removedStatus ? (entry as T) : null
}

// The retry run named by the entry, when it names one.
export function retryRunOf(entry: unknown): number | undefined {
  if (typeof entry !== 'object' || entry === null) return undefined
  const run = (entry as { retry_run_id?: unknown }).retry_run_id
  return typeof run === 'number' && Number.isInteger(run) && run > 0 ? run : undefined
}

// A notice for an item whose row is gone begins with its target: "{T}: ".
export interface RemovalNoticeData {
  id: number
  target?: string
  runId?: number
}
