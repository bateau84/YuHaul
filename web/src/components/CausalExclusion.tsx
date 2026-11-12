import type { CausalExclusion as Cause, RouteDecisionSummary } from '../types'
import { routeAttribution } from '../lib/route-attribution'

function reference(cause: Cause | null | undefined): string | undefined {
  const id = cause?.config_version_id
  const index = cause?.exclusion_index
  if (typeof id !== 'number' || !Number.isSafeInteger(id) || id <= 0 ||
      typeof index !== 'number' || !Number.isSafeInteger(index) || index < 0 || index >= Number.MAX_SAFE_INTEGER) return undefined
  return `configuration ${id}, exclusion #${index + 1}`
}

function comparison(cause: Cause): string | undefined {
  // Shown exactly as configured; rendered by React as text, never as markup.
  const value = cause.configured_value_status === 'available' && typeof cause.configured_value === 'string'
    ? cause.configured_value
    : 'value unavailable'
  switch (`${cause.exclusion_type}/${cause.field}/${cause.comparison}`) {
    case 'channel/channel_id/exact': return `Channel ID exactly equals configured channel ID “${value}”`
    case 'video/video_id/exact': return `Video ID exactly equals configured video ID “${value}”`
    case 'title/title/trimmed_equal_fold': return `Title equals configured title “${value}”, ignoring surrounding whitespace and case`
    case 'word/title/lowercase_substring': return `Title contains configured substring “${value}”, ignoring case`
    case 'duration_min/duration_seconds/strict_less_than': return `Duration is strictly less than configured minimum ${value} seconds`
    case 'duration_max/duration_seconds/strict_greater_than': return `Duration is strictly greater than configured maximum ${value} seconds`
    default: return undefined
  }
}

export function CausalExclusion({ decision, showRoute = false }: { decision: RouteDecisionSummary; showRoute?: boolean }) {
  const cause = decision.causal_exclusion
  const ref = reference(cause)
  const sentence = cause?.status === 'available' && cause.semantics_version === 1 && cause.matched === true && ref
    ? comparison(cause) : undefined
  let incomplete: string
  switch (cause?.status) {
    case undefined:
    case 'not_recorded': incomplete = 'Route exclusion recorded; rule unavailable. This explanation is incomplete.'; break
    case 'definition_unavailable': incomplete = 'Route exclusion recorded; the captured rule definition is unavailable. This explanation is incomplete.'; break
    case 'invalid_reference': incomplete = 'Route exclusion recorded; the captured rule reference is invalid. This explanation is incomplete.'; break
    default: incomplete = 'Route exclusion recorded; the captured rule cannot be interpreted by this version. This explanation is incomplete.'
  }
  return <div style={{ overflowWrap: 'anywhere' }}>
    {showRoute && <p>{routeAttribution(decision)}</p>}
    {showRoute && <p>Skipped — excluded from this route.</p>}
    {sentence ? <>
      <p>Recorded causal exclusion: {ref}.</p>
      <p>Matched rule: {sentence}.</p>
      {(cause?.configured_value_status !== 'available' || typeof cause?.configured_value !== 'string') && <p>Configured value unavailable.</p>}
      <p>Conditions were not evaluated for this route because this exclusion matched.</p>
    </> : <>
      <p>{incomplete}</p>
      {ref && <p>Captured reference: {ref}.</p>}
      {cause?.status === 'definition_unavailable' && comparison(cause) && <p>Captured rule: {comparison(cause)}.</p>}
    </>}
    <p>Compared input unavailable in this rule-reference record.</p>
  </div>
}
