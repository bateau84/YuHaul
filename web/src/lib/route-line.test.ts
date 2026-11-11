import { describe, expect, it } from 'vitest'
import type { CausalExclusion, FailedCondition, RouteDecisionSummary } from '../types'
import { conditionNeed, detailsButtonName, exclusionReason, noMatchReason, routeLabel, routeLine, truncateValue } from './route-line'

const ID = 'a3f5b2c1-1111-4222-8333-444455556666'

const base: RouteDecisionSummary = {
  id: ID, route_id: ID, route_name: 'New Listens', route_name_source: 'run_snapshot',
  evaluation_order: 0, decision: 'matched',
}

const cause = (over: Partial<CausalExclusion> = {}): CausalExclusion => ({
  status: 'available', config_version_id: 42, exclusion_index: 0, semantics_version: 1,
  exclusion_type: 'duration_min', field: 'duration_seconds', comparison: 'strict_less_than',
  configured_value: '75', configured_value_status: 'available', matched: true, compared_input_status: 'unavailable', ...over,
})

const failed = (over: Partial<FailedCondition> = {}): FailedCondition => ({
  field: 'channel_name', operator: 'equals', value: 'Fireship', composition: 'AND', condition_count: 1, further_failed: 0, ...over,
})

describe('route label', () => {
  it('shows the first 8 characters of the route UUID by attribution', () => {
    expect(routeLabel(base)).toBe('New Listens (a3f5b2c1)')
    expect(routeLabel({ ...base, route_name_source: 'current_local' })).toBe('New Listens (current name, a3f5b2c1)')
    expect(routeLabel({ ...base, route_name_source: 'unavailable' })).toBe('Route name unavailable (a3f5b2c1)')
    expect(routeLabel({ ...base, route_name_source: 'mystery' })).toBe('Route name unavailable (a3f5b2c1)')
    expect(routeLabel({ ...base, route_name: '' })).toBe('Route name unavailable (a3f5b2c1)')
  })
  it('names the Details control after the route, containing its visible label', () => {
    expect(detailsButtonName(base)).toBe('Details for route New Listens (a3f5b2c1)')
    expect(detailsButtonName({ ...base, route_name_source: 'unavailable' })).toBe('Details for route name unavailable (a3f5b2c1)')
    expect(detailsButtonName(base)).toContain('Details')
  })
})

describe('exclusion vocabulary (visible / spoken)', () => {
  it.each([
    ['channel', 'channel_id', 'exact', 'UC1', 'channel ID = “UC1”', 'channel ID equals UC1'],
    ['video', 'video_id', 'exact', 'abc', 'video ID = “abc”', 'video ID equals abc'],
    ['title', 'title', 'trimmed_equal_fold', 'Hello', 'title = “Hello” (any case)', 'title equals Hello, any case'],
    ['word', 'title', 'lowercase_substring', 'asmr', 'title contains “asmr” (any case)', 'title contains asmr, any case'],
    ['duration_min', 'duration_seconds', 'strict_less_than', '75', 'duration < 75 s', 'duration less than 75 seconds'],
    ['duration_max', 'duration_seconds', 'strict_greater_than', '3600', 'duration > 3600 s', 'duration greater than 3600 seconds'],
  ])('%s', (type, field, comparison, value, visible, spoken) => {
    expect(exclusionReason(cause({ exclusion_type: type, field, comparison, configured_value: value }))).toEqual({ visible, spoken })
  })

  it('uses the spoken wording, no symbol, beside a missing value', () => {
    expect(exclusionReason(cause({ configured_value: null, configured_value_status: 'withheld' })))
      .toEqual({ visible: 'duration less than (value unavailable for display)', spoken: 'duration less than (value unavailable for display)' })
    expect(exclusionReason(cause({ configured_value: null, configured_value_status: 'unavailable' })).visible)
      .toBe('duration less than (value unavailable)')
  })

  it.each([
    [undefined, 'rule unavailable (record incomplete)'],
    [null, 'rule unavailable (record incomplete)'],
    [cause({ status: 'not_recorded' }), 'rule unavailable (record incomplete)'],
    [cause({ status: 'definition_unavailable' }), 'rule definition unavailable (record incomplete)'],
    [cause({ status: 'unsupported' }), 'rule not readable by this version (record incomplete)'],
    [cause({ status: 'some_future_status' }), 'rule not readable by this version (record incomplete)'],
    [cause({ comparison: 'some_future_comparison' }), 'rule not readable by this version (record incomplete)'],
    [cause({ semantics_version: 2 }), 'rule not readable by this version (record incomplete)'],
    [cause({ status: 'invalid_reference' }), 'rule reference invalid (record incomplete)'],
  ])('incomplete state %#', (c, text) => {
    const reason = exclusionReason(c as CausalExclusion | null | undefined)
    expect(reason.visible).toBe(text)
    expect(reason.visible).not.toMatch(/some_future|not captured/)
  })

  it('does not present a non-numeric duration value as a rule', () => {
    expect(exclusionReason(cause({ configured_value: '75; drop' })).visible).toBe('rule not readable by this version (record incomplete)')
  })
})

describe('truncation to 40 characters', () => {
  it('keeps a 40-character value whole and cuts a longer one with …', () => {
    const forty = 'a'.repeat(40)
    expect(truncateValue(forty)).toBe(forty)
    expect(truncateValue(forty + 'b')).toBe(forty + '…')
  })
  it('counts code points, never splitting a surrogate pair', () => {
    const cut = truncateValue('🎵'.repeat(41))
    expect(cut).toBe('🎵'.repeat(40) + '…')
  })
  it('marks a cut value inside the quotes in a rule and a condition', () => {
    const long = 'x'.repeat(50)
    expect(exclusionReason(cause({ exclusion_type: 'word', field: 'title', comparison: 'lowercase_substring', configured_value: long })).visible)
      .toBe(`title contains “${'x'.repeat(40)}…” (any case)`)
    expect(noMatchReason(failed({ operator: 'contains', value: long }))?.visible)
      .toBe(`needs channel name contains “${'x'.repeat(40)}…”`)
  })
})

describe('condition vocabulary', () => {
  it.each([
    ['title', 'contains', 'jazz', 'title contains “jazz”', 'title contains jazz'],
    ['channel_name', 'equals', 'Fireship', 'channel name = “Fireship”', 'channel name equals Fireship'],
    ['description', 'starts_with', 'Intro', 'description starts with “Intro”', 'description starts with Intro'],
    ['channel_id', 'ends_with', 'xyz', 'channel ID ends with “xyz”', 'channel ID ends with xyz'],
    ['title', 'matches_regex', '^a+$', 'title matches pattern ^a+$', 'title matches pattern ^a+$'],
    ['duration_seconds', 'equals', '90', 'duration = 90 s', 'duration equals 90 seconds'],
    ['duration_seconds', 'greater_than', '300', 'duration > 300 s', 'duration greater than 300 seconds'],
    ['duration_seconds', 'less_than', '60', 'duration < 60 s', 'duration less than 60 seconds'],
    ['duration_seconds', 'between', '60,600', 'duration 60–600 s', 'duration between 60 and 600 seconds'],
  ])('%s %s', (field, operator, value, visible, spoken) => {
    expect(conditionNeed(failed({ field, operator, value }))).toEqual({ visible, spoken })
  })

  it.each([
    ['unknown operator', { operator: 'weird' }],
    ['unknown field', { field: 'secret' }],
    ['text operator on duration', { field: 'duration_seconds', operator: 'contains', value: '3' }],
    ['numeric operator on text', { operator: 'greater_than', value: '3' }],
    ['non-numeric duration', { field: 'duration_seconds', operator: 'equals', value: 'abc' }],
    ['malformed between', { field: 'duration_seconds', operator: 'between', value: '60' }],
  ])('falls back for %s without echoing it', (_name, over) => {
    expect(conditionNeed(failed(over))).toBeUndefined()
    expect(noMatchReason(failed(over))).toBeUndefined()
  })

  it('shows a missing value as unavailable', () => {
    expect(noMatchReason(failed({ value: null }))?.visible).toBe('needs channel name equals (value unavailable)')
  })

  it('adds (+n more) for further failed AND conditions, spoken as “plus”', () => {
    expect(noMatchReason(failed({ further_failed: 2 }))).toEqual({
      visible: 'needs channel name = “Fireship” (+2 more)',
      spoken: 'needs channel name equals Fireship (plus 2 more)',
    })
  })

  it('says none of k conditions matched under OR', () => {
    expect(noMatchReason(failed({ composition: 'OR', condition_count: 3 }))?.visible).toBe('none of 3 conditions matched')
  })

  it('has no reason when the list read carries none', () => {
    expect(noMatchReason(undefined)).toBeUndefined()
    expect(noMatchReason(null)).toBeUndefined()
  })
})

describe('whole line', () => {
  it.each([
    [{ decision: 'matched' }, 'matched', undefined],
    [{ decision: 'not_matched' }, 'no match', undefined],
    [{ decision: 'not_matched', failed_condition: failed() }, 'no match', 'needs channel name = “Fireship”'],
    [{ decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: cause() }, 'excluded', 'duration < 75 s'],
    [{ decision: 'skipped', skip_reason: 'final_route_matched' }, 'skipped', 'an earlier final route matched'],
    [{ decision: 'skipped', skip_reason: 'disabled' }, 'skipped', 'route disabled'],
    [{ decision: 'deferred' }, 'deferred', undefined],
  ])('%j', (over, outcome, reason) => {
    const line = routeLine({ ...base, ...over } as RouteDecisionSummary)
    expect(line.outcome).toBe(outcome)
    expect(line.reason?.visible).toBe(reason)
  })
})
