// Plain-language Reason for a stored failure (design: "Errors — Category and
// Reason Presentation"). The sentence is chosen from the closed `diag:v1`
// fields `code`, `status` and `reason`, never from prose. A string that does
// not pass the grammar, an unlisted code and run-level codes read
// "Reason unavailable." The raw value is never a sentence; it is available only
// as the Support code inside a "Technical details" disclosure.

export const REASON_UNAVAILABLE = 'Reason unavailable.'

const DIAG =
  /^diag:v1 phase=([a-z_]+) operation=([a-z_]+) code=([a-z_]+)(?: status=([0-9]+))?(?: reason=([A-Za-z_]+))?(?: sqlite=([A-Z_]+))?$/

// The closed value sets of internal/diag (run-outcome diagnostics ARS-001).
// failure-reason.test.ts compares them with diag.go, so a drift fails a test.
const set = (s: string) => new Set(s.split(' '))
export const DIAG_PHASES = set('unknown discovery exclusion duplicate_detection routing action_execution')
export const DIAG_OPERATIONS = set(
  'unknown start_run load_run load_token select_inputs capture_selection discover load_metadata apply_exclusions detect_duplicates evaluate_video record_decision execute_action record_action retain_failure retry finalize_run record_phase inspect_run inspect_queue account_quota generate_advisory parse_response subscriptions_list channels_list playlist_items_list videos_list playlist_items_insert playlists_list refresh_token save_token account_status disconnect_account oauth_callback dispatch_request discard_run stop_run end_legacy_run requeue_stranded video_search',
)
export const DIAG_PROVIDER_REASONS = set(
  'quotaExceeded dailyLimitExceeded rateLimitExceeded userRateLimitExceeded authError forbidden insufficientPermissions backendError internalError badRequest invalidParameter notFound playlistNotFound videoNotFound playlistItemsNotAccessible playlistContainsMaximumNumberOfVideos videoAlreadyInPlaylist',
)
export const DIAG_OAUTH_REASONS = set(
  'invalid_request invalid_client invalid_grant unauthorized_client unsupported_grant_type invalid_scope access_denied temporarily_unavailable server_error',
)
export const DIAG_SQLITE = set(
  'SQLITE_ERROR SQLITE_BUSY SQLITE_LOCKED SQLITE_READONLY SQLITE_IOERR SQLITE_CORRUPT SQLITE_FULL SQLITE_CANTOPEN SQLITE_CONSTRAINT',
)
export const DIAG_CODES = set(
  'unknown_error sqlite_error provider_error oauth_error timeout canceled transport_error invalid_provider_response panic authorization_unavailable action_authority_invalid credential_persistence_failed authorization_changed mutation_application_uncertain snapshot_unavailable snapshot_unreadable checkpoint_unrecognized stopped_by_user ended_by_user video_unavailable original_decision_unavailable no_source_scanned',
)
const DISCARD_CODES = new Set(['snapshot_unavailable', 'snapshot_unreadable', 'checkpoint_unrecognized'])

export interface DiagFacts {
  code: string
  status?: string
  reason?: string
}

// A string is a diag:v1 string only when its shape and every value are in the
// closed sets, as internal/diag admits it (Facts.Valid). Anything else is null.
export function parseDiag(message: string | null | undefined): DiagFacts | null {
  const m = message ? DIAG.exec(message) : null
  if (!m) return null
  const [, phase, operation, code, status, reason, sqlite] = m
  if (!DIAG_PHASES.has(phase) || !DIAG_OPERATIONS.has(operation) || !DIAG_CODES.has(code)) return null
  if (code === 'original_decision_unavailable') {
    return phase === 'action_execution' && operation === 'load_run' && !status && !reason && !sqlite ? { code } : null
  }
  if (code === 'no_source_scanned') {
    return phase === 'discovery' && operation === 'discover' && !status && !reason && !sqlite ? { code } : null
  }
  if (code === 'video_unavailable') {
    return phase === 'routing' && operation === 'load_metadata' && !status && !reason && !sqlite ? { code } : null
  }
  if (['discard_run', 'stop_run', 'end_legacy_run'].includes(operation) || DISCARD_CODES.has(code) || code === 'stopped_by_user' || code === 'ended_by_user') {
    const ok =
      (operation === 'discard_run' && DISCARD_CODES.has(code)) ||
      (operation === 'stop_run' && code === 'stopped_by_user') ||
      (operation === 'end_legacy_run' && code === 'ended_by_user')
    return ok && !status && !reason && !sqlite ? { code } : null
  }
  if (status) {
    const n = Number(status)
    if (String(n) !== status) return null
    if (code === 'provider_error' ? n < 300 || n > 599 : code === 'oauth_error' ? n < 100 || n > 599 : true) return null
  }
  if (['credential_persistence_failed', 'authorization_changed', 'mutation_application_uncertain'].includes(code) && (status || reason || sqlite)) return null
  if (reason && !((code === 'provider_error' && DIAG_PROVIDER_REASONS.has(reason)) || (code === 'oauth_error' && DIAG_OAUTH_REASONS.has(reason)))) return null
  if (sqlite && !(code === 'sqlite_error' && DIAG_SQLITE.has(sqlite))) return null
  return { code, status, reason }
}

// Codes whose cause the person can act on in Settings > Account. Errors rows
// with one of them offer "View account status".
const ACCOUNT_CODES = new Set(['oauth_error', 'authorization_unavailable', 'authorization_changed', 'credential_persistence_failed'])
export function offersAccountStatus(message: string | null | undefined): boolean {
  const facts = parseDiag(message)
  return facts !== null && ACCOUNT_CODES.has(facts.code)
}

const PROVIDER_BY_REASON: Record<string, string> = {
  quotaExceeded: "YouTube's API quota was used up.",
  dailyLimitExceeded: "YouTube's API quota was used up.",
  rateLimitExceeded: 'YouTube asked YuHaul to slow down.',
  userRateLimitExceeded: 'YouTube asked YuHaul to slow down.',
  authError: "YouTube did not accept YuHaul's access for this request.",
  forbidden: "YouTube did not accept YuHaul's access for this request.",
  insufficientPermissions: "YouTube did not accept YuHaul's access for this request.",
  backendError: 'YouTube reported a problem on its side.',
  internalError: 'YouTube reported a problem on its side.',
  badRequest: 'YouTube rejected the request as invalid.',
  invalidParameter: 'YouTube rejected the request as invalid.',
  videoNotFound: 'YouTube could not find this video.',
  playlistNotFound: 'YouTube could not find the playlist.',
  playlistItemsNotAccessible: "YouTube would not let YuHaul read the playlist's items.",
  notFound: 'YouTube could not find what was requested.',
  playlistContainsMaximumNumberOfVideos: 'YouTube reported that the playlist is full.',
  videoAlreadyInPlaylist: 'YouTube reported that the video is already in the playlist.',
}

const BY_CODE: Record<string, string> = {
  authorization_unavailable: 'No usable YouTube access was available.',
  authorization_changed: 'YouTube access changed while this was in progress.',
  credential_persistence_failed: 'YuHaul could not save renewed YouTube access.',
  action_authority_invalid: "YuHaul could not confirm that this run's saved routes allow this playlist change.",
  mutation_application_uncertain:
    'YuHaul could not confirm whether this playlist action was applied. Repeating it could add the video again, so it remains blocked.',
  no_source_scanned: 'No channels were scanned successfully.',
  video_unavailable: 'YouTube did not return this video when YuHaul asked for its details.',
  timeout: 'YuHaul stopped waiting because a step took too long.',
  transport_error: 'A network connection failed.',
  invalid_provider_response: "YouTube's answer could not be understood.",
  sqlite_error: 'YuHaul could not read or save its own data.',
  canceled: 'The work was cancelled before YuHaul got a result.',
  stopped_by_user: 'Stopped by you before it finished.',
  ended_by_user: 'Marked as ended by you.',
  panic: 'YuHaul hit an unexpected internal fault.',
  unknown_error: 'The cause of this failure is not available.',
}

// The sentence for a stored failure string; never the raw text.
export function failureSentence(message: string | null | undefined): string {
  const facts = parseDiag(message)
  if (!facts) return REASON_UNAVAILABLE
  switch (facts.code) {
    case 'provider_error': {
      if (facts.reason && Object.hasOwn(PROVIDER_BY_REASON, facts.reason)) return PROVIDER_BY_REASON[facts.reason]
      return facts.status
        ? `YouTube returned an error (status ${facts.status}).`
        : 'YouTube returned an error.'
    }
    case 'oauth_error':
      return facts.reason === 'invalid_grant'
        ? "YouTube no longer accepts YuHaul's saved access."
        : 'YouTube access could not be renewed.'
    default:
      return Object.hasOwn(BY_CODE, facts.code) ? BY_CODE[facts.code] : REASON_UNAVAILABLE
  }
}

// The Errors Category cell. Any value this version does not list reads "Error".
export function categoryLabel(category: string | null | undefined): string {
  switch (category) {
    case 'api_error':
      return 'YouTube API error'
    case 'authorization':
      return 'Authorization'
    case 'runtime':
      return 'Processing error'
    default:
      return 'Error'
  }
}
