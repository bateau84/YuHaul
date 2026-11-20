import { describe, it, expect } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { CausalExclusion } from './CausalExclusion'
import type { CausalExclusion as Cause, RouteDecisionSummary } from '../types'

const cause: Cause = { status: 'available', config_version_id: 42, exclusion_index: 0, semantics_version: 1, exclusion_type: 'title', field: 'title', comparison: 'trimmed_equal_fold', configured_value: 'live', configured_value_status: 'available', matched: true, compared_input_status: 'unavailable' }
const decision: RouteDecisionSummary = { id: 'uuid', route_id: 'uuid', route_name: 'Route A', route_name_source: 'run_snapshot', evaluation_order: 0, decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: cause }

describe('causal exclusion presentation', () => {
  it.each([
    ['channel', 'channel_id', 'exact', 'Channel ID exactly equals configured channel ID “live”'],
    ['video', 'video_id', 'exact', 'Video ID exactly equals configured video ID “live”'],
    ['title', 'title', 'trimmed_equal_fold', 'Title equals configured title “live”, ignoring surrounding whitespace and case'],
    ['word', 'title', 'lowercase_substring', 'Title contains configured substring “live”, ignoring case'],
    ['duration_min', 'duration_seconds', 'strict_less_than', 'Duration is strictly less than configured minimum live seconds'],
    ['duration_max', 'duration_seconds', 'strict_greater_than', 'Duration is strictly greater than configured maximum live seconds'],
  ])('renders %s comparison', (type, field, comparison, expected) => {
    render(<CausalExclusion decision={{ ...decision, causal_exclusion: { ...cause, exclusion_type: type, field, comparison } }} showRoute />)
    expect(screen.getByText(`Matched rule: ${expected}.`)).toBeTruthy()
    expect(screen.getByText('Recorded causal exclusion: configuration 42, exclusion #1.')).toBeTruthy()
    expect(screen.getByText('Route: Route A (ID: uuid)')).toBeTruthy()
    cleanup()
  })

  it('labels current route name', () => {
    render(<CausalExclusion decision={{ ...decision, route_name_source: 'current_local' }} showRoute />)
    expect(screen.getByText('Route: Route A (current/local name; ID: uuid)')).toBeTruthy()
    cleanup()
  })

  it.each([
    '<img src=x onerror=alert(1)> & <b>bold</b>',
    `say "hi" and 'bye' \\ back`,
    'Ünïcödé 日本語 🎵 café',
    'x'.repeat(500),
  ])('shows configured value %j exactly as text, never as markup', (value) => {
    const { container } = render(<CausalExclusion decision={{ ...decision, causal_exclusion: { ...cause, exclusion_type: 'word', comparison: 'lowercase_substring', configured_value: value } }} showRoute />)
    expect(screen.getByText(`Matched rule: Title contains configured substring “${value}”, ignoring case.`)).toBeTruthy()
    expect(container.querySelector('img, b')).toBeNull()
    expect(screen.queryByText('Configured value unavailable.')).toBeNull()
    cleanup()
  })

  it('keeps identity, type and comparison when the rule cannot be resolved', () => {
    render(<CausalExclusion decision={{ ...decision, causal_exclusion: { ...cause, status: 'definition_unavailable', exclusion_type: 'word', field: 'title', comparison: 'lowercase_substring', configured_value: null, configured_value_status: 'unavailable', matched: null } }} />)
    expect(screen.getByText('Captured reference: configuration 42, exclusion #1.')).toBeTruthy()
    expect(screen.getByText('Captured rule: Title contains configured substring “value unavailable”, ignoring case.')).toBeTruthy()
    cleanup()
  })

  it('treats a legacy withheld status as unavailable, not as a value', () => {
    render(<CausalExclusion decision={{ ...decision, causal_exclusion: { ...cause, configured_value_status: 'withheld', configured_value: 'secret' } }} />)
    expect(screen.getByText('Configured value unavailable.')).toBeTruthy()
    cleanup()
  })

  it.each([
    [undefined, 'Route exclusion recorded; rule unavailable. This explanation is incomplete.'],
    ['definition_unavailable', 'Route exclusion recorded; the captured rule definition is unavailable. This explanation is incomplete.'],
    ['invalid_reference', 'Route exclusion recorded; the captured rule reference is invalid. This explanation is incomplete.'],
    ['future', 'Route exclusion recorded; the captured rule cannot be interpreted by this version. This explanation is incomplete.'],
  ])('shows incomplete state %s', (status, text) => {
    render(<CausalExclusion decision={{ ...decision, causal_exclusion: status ? { ...cause, status } : undefined }} />)
    expect(screen.getByText(text)).toBeTruthy()
    expect(screen.getByText('Compared input unavailable in this rule-reference record.')).toBeTruthy()
    cleanup()
  })
})
