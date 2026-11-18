import type { DeferredItem, ErrorEntry } from '../types'

// Copy for BR-039 (design: "Final Outcomes Are Never Reprocessed — Run Detail,
// Queue and Statistics Copy"). The UI branches on machine values
// (closed_reason, video_final_outcome, code), never on message text. An
// unknown outcome value is never shown raw.

const NAMES: Partial<Record<string, string>> = {
  accepted: 'Accepted',
  rejected: 'Rejected',
  excluded: 'Excluded',
  unavailable_on_youtube: 'Unavailable on YouTube',
}

// The four final outcomes a queue entry can be closed under; null for anything else.
export function finalOutcomeName(outcome: string | null | undefined): string | null {
  return (outcome && NAMES[outcome]) || null
}

export function isClosedEntry(entry: Pick<ErrorEntry | DeferredItem, 'closed_reason'>): boolean {
  return entry.closed_reason === 'final_outcome'
}

// The 409 video_already_final refusal shown in place of the row.
export function refusalText(outcome: string | null | undefined): string {
  const name = finalOutcomeName(outcome)
  return `Retry did nothing. This video already has a final outcome${name ? `: ${name}` : ''}. It was not processed again, and nothing was sent to YouTube. This item is now closed.`
}

export function closedText(outcome: string | null | undefined): string {
  const name = finalOutcomeName(outcome)
  return `Closed. This video already has a final outcome${name ? `: ${name}` : ''}. It was not processed again.`
}

// Keeps a refused entry's identity and marks it closed. Used when the response
// carries an entry that is usable (a matching id), or builds the closed form of
// the row the person pressed Retry on.
export function usableEntry<T extends { id: number }>(entry: unknown, id: number): T | null {
  if (typeof entry !== 'object' || entry === null) return null
  const e = entry as { id?: unknown; closed_reason?: unknown }
  return e.id === id && e.closed_reason === 'final_outcome' ? (entry as T) : null
}

// The closed diag code of a stored error message ("diag:v1 phase=… code=…").
export function diagCode(message: string | null | undefined): string | null {
  if (!message || !message.startsWith('diag:v1 ')) return null
  for (const field of message.split(/\s+/)) {
    if (field.startsWith('code=')) return field.slice('code='.length)
  }
  return null
}

export const ORIGINAL_DECISION_UNAVAILABLE = 'original_decision_unavailable'

export const originalDecisionUnavailable = {
  type: 'Original decision unavailable',
  detail:
    "This video already has a playlist addition that YuHaul sent in an earlier run. YuHaul cannot read that run's saved decision, so it cannot safely send the remaining additions. Nothing was sent now.",
  noRetry: 'Retry is not offered: it would fail the same way.',
  dismissTitle: 'Dismiss this item from the error queue?',
  dismissBody:
    "YuHaul cannot finish this video's remaining playlist additions, so dismissing it means they will not be sent. The historical record will be preserved.",
  dismissedToast: 'Item dismissed from error queue',
  videoReason: "Playlist additions not finished: the earlier run's decision could not be read.",
} as const
