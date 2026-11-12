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

export function discardReasonSentence(cause: string | undefined | null): string {
  return (cause && REASON_SENTENCES[cause]) || FALLBACK_REASON
}
