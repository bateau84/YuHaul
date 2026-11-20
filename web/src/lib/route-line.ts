import type { CausalExclusion, FailedCondition, RouteDecisionSummary } from '../types'

// Vocabulary for the compact route lines on the Run Detail list (design:
// "Compact route lines on the Run Detail list"). Every text has a visible form
// and a spoken form so symbols (=, <, >, –) never reach assistive technology.

export interface Piece {
  visible: string
  spoken: string
}

const same = (text: string): Piece => ({ visible: text, spoken: text })

export const MAX_VALUE_CHARS = 40

// Cut by code point so a surrogate pair is never split. A cut value ends in …
// so it is never presented as complete.
export function truncateValue(value: string): string {
  const chars = Array.from(value)
  return chars.length > MAX_VALUE_CHARS ? `${chars.slice(0, MAX_VALUE_CHARS).join('')}…` : value
}

const quoted = (value: string) => `“${truncateValue(value)}”`

export function shortRouteId(decision: RouteDecisionSummary): string {
  return decision.route_id.slice(0, 8)
}

export function routeLabel(decision: RouteDecisionSummary): string {
  const id = shortRouteId(decision)
  if (decision.route_name && decision.route_name_source === 'run_snapshot') return `${decision.route_name} (${id})`
  if (decision.route_name && decision.route_name_source === 'current_local') return `${decision.route_name} (current name, ${id})`
  return `Route name unavailable (${id})`
}

export function detailsButtonName(decision: RouteDecisionSummary): string {
  const id = shortRouteId(decision)
  const named = decision.route_name && (decision.route_name_source === 'run_snapshot' || decision.route_name_source === 'current_local')
  return named ? `Details for route ${decision.route_name} (${id})` : `Details for route name unavailable (${id})`
}

// --- Exclusion rules -------------------------------------------------------

interface ExclusionForm {
  textual: boolean
  visible: (v: string) => string
  spoken: (v: string, known: boolean) => string
}

const exclusionForms: Record<string, ExclusionForm> = {
  'channel/channel_id/exact': { textual: true, visible: (v) => `channel ID = ${v}`, spoken: (v) => `channel ID equals ${v}` },
  'video/video_id/exact': { textual: true, visible: (v) => `video ID = ${v}`, spoken: (v) => `video ID equals ${v}` },
  'title/title/trimmed_equal_fold': { textual: true, visible: (v) => `title = ${v} (any case)`, spoken: (v) => `title equals ${v}, any case` },
  'word/title/lowercase_substring': { textual: true, visible: (v) => `title contains ${v} (any case)`, spoken: (v) => `title contains ${v}, any case` },
  'duration_min/duration_seconds/strict_less_than': { textual: false, visible: (v) => `duration < ${v} s`, spoken: (v, known) => `duration less than ${v}${known ? ' seconds' : ''}` },
  'duration_max/duration_seconds/strict_greater_than': { textual: false, visible: (v) => `duration > ${v} s`, spoken: (v, known) => `duration greater than ${v}${known ? ' seconds' : ''}` },
}

const WHOLE_NUMBER = /^\d+$/

function exclusionReference(cause: CausalExclusion): boolean {
  const id = cause.config_version_id
  const index = cause.exclusion_index
  return typeof id === 'number' && Number.isSafeInteger(id) && id > 0 &&
    typeof index === 'number' && Number.isSafeInteger(index) && index >= 0 && index < Number.MAX_SAFE_INTEGER
}

// The reason shown after “excluded:”. Mirrors the full CausalExclusion states:
// only a supported, matched, referenced rule is shown as a rule; every other
// state says what is incomplete and never echoes an unknown raw value.
export function exclusionReason(cause: CausalExclusion | null | undefined): Piece {
  if (!cause) return same('rule unavailable (record incomplete)')
  switch (cause.status) {
    case 'not_recorded':
      return same('rule unavailable (record incomplete)')
    case 'definition_unavailable':
      return same('rule definition unavailable (record incomplete)')
    case 'invalid_reference':
      return same('rule reference invalid (record incomplete)')
  }
  const unreadable = same('rule not readable by this version (record incomplete)')
  if (cause.status !== 'available' || cause.semantics_version !== 1 || cause.matched !== true || !exclusionReference(cause)) return unreadable
  const form = exclusionForms[`${cause.exclusion_type}/${cause.field}/${cause.comparison}`]
  if (!form) return unreadable
  if (cause.configured_value_status !== 'available' || typeof cause.configured_value !== 'string') {
    const text = cause.configured_value_status === 'withheld' ? '(value unavailable for display)' : '(value unavailable)'
    return same(form.spoken(text, false))
  }
  const value = cause.configured_value
  if (!form.textual && !WHOLE_NUMBER.test(value)) return unreadable
  const shown = form.textual ? quoted(value) : value
  return { visible: form.visible(shown), spoken: form.spoken(truncateValue(value), true) }
}

// --- Failed conditions -----------------------------------------------------

const TEXT_FIELDS: Record<string, string> = {
  title: 'title',
  channel_name: 'channel name',
  description: 'description',
  channel_id: 'channel ID',
}
const TEXT_OPERATORS = new Set(['contains', 'equals', 'starts_with', 'ends_with', 'matches_regex'])
const DURATION_OPERATORS = new Set(['equals', 'greater_than', 'less_than', 'between'])
const NUMBER = /^-?\d+(\.\d+)?$/

const UNAVAILABLE = '(value unavailable)'

// What the route needs, as a piece. Undefined when field, operator or value
// are outside the accepted vocabulary, so the line falls back to “no match”.
export function conditionNeed(c: FailedCondition): Piece | undefined {
  const known = typeof c.value === 'string'
  const value = known ? (c.value as string) : UNAVAILABLE
  if (c.field === 'duration_seconds') {
    if (!DURATION_OPERATORS.has(c.operator)) return undefined
    if (c.operator === 'between') {
      if (!known) return same('duration between (value unavailable)')
      const parts = value.split(',').map((p) => p.trim())
      if (parts.length !== 2 || !parts.every((p) => NUMBER.test(p))) return undefined
      return { visible: `duration ${parts[0]}–${parts[1]} s`, spoken: `duration between ${parts[0]} and ${parts[1]} seconds` }
    }
    if (known && !NUMBER.test(value)) return undefined
    const word = { equals: ['=', 'equals'], greater_than: ['>', 'greater than'], less_than: ['<', 'less than'] }[c.operator]!
    if (!known) return same(`duration ${word[1]} ${UNAVAILABLE}`)
    return { visible: `duration ${word[0]} ${value} s`, spoken: `duration ${word[1]} ${value} seconds` }
  }
  const field = TEXT_FIELDS[c.field]
  if (!field || !TEXT_OPERATORS.has(c.operator)) return undefined
  const shown = known ? truncateValue(value) : value
  const q = known ? quoted(value) : UNAVAILABLE
  // Quotes are shown only around a real value; the spoken form never has them.
  const worded = (verb: string): Piece => ({ visible: `${field} ${verb} ${q}`, spoken: `${field} ${verb} ${shown}` })
  switch (c.operator) {
    case 'contains': return worded('contains')
    case 'equals': return known ? { visible: `${field} = ${q}`, spoken: `${field} equals ${shown}` } : same(`${field} equals ${UNAVAILABLE}`)
    case 'starts_with': return worded('starts with')
    case 'ends_with': return worded('ends with')
    default: return same(`${field} matches pattern ${shown}`)
  }
}

// The reason shown after “no match:”, or undefined when the list read cannot
// say (the line then reads “— no match”).
export function noMatchReason(c: FailedCondition | null | undefined): Piece | undefined {
  if (!c) return undefined
  if (c.composition === 'OR') {
    // Every condition failed under OR; the details hold them.
    if (!Number.isSafeInteger(c.condition_count) || c.condition_count < 1) return undefined
    return same(`none of ${c.condition_count} conditions matched`)
  }
  const need = conditionNeed(c)
  if (!need) return undefined
  const more = Number.isSafeInteger(c.further_failed) && c.further_failed > 0 ? c.further_failed : 0
  return {
    visible: `needs ${need.visible}${more ? ` (+${more} more)` : ''}`,
    spoken: `needs ${need.spoken}${more ? ` (plus ${more} more)` : ''}`,
  }
}

// Full, as-recorded text for the expanded details.
export function failedConditionDetail(c: FailedCondition): string {
  const value = typeof c.value === 'string' ? `“${c.value}”` : UNAVAILABLE
  return `${c.field} ${c.operator} ${value} — not met`
}

// --- Whole line ------------------------------------------------------------

export type LineKind = 'matched' | 'no_match' | 'excluded' | 'skipped' | 'deferred' | 'unknown'

export interface RouteLine {
  kind: LineKind
  outcome: string
  reason?: Piece
}

export function routeLine(d: RouteDecisionSummary): RouteLine {
  switch (d.decision) {
    case 'matched':
      return { kind: 'matched', outcome: 'matched' }
    case 'not_matched':
      return { kind: 'no_match', outcome: 'no match', reason: noMatchReason(d.failed_condition) }
    case 'deferred':
      return { kind: 'deferred', outcome: 'deferred' }
    case 'skipped':
      if (d.skip_reason === 'exclusion') return { kind: 'excluded', outcome: 'excluded', reason: exclusionReason(d.causal_exclusion) }
      if (d.skip_reason === 'final_route_matched') return { kind: 'skipped', outcome: 'skipped', reason: same('an earlier final route matched') }
      if (d.skip_reason === 'disabled') return { kind: 'skipped', outcome: 'skipped', reason: same('route disabled') }
      return { kind: 'skipped', outcome: 'skipped' }
    default:
      return { kind: 'unknown', outcome: 'outcome unavailable' }
  }
}
