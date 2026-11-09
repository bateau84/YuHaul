import { describe, expect, it } from 'vitest'
import { currentLocalObservedAt, currentLocalUnavailableLabel } from './current-local-metadata'

describe('currentLocalObservedAt', () => {
  it.each([
    '2026-11-07T10:15:30Z',
    '2024-02-29T23:59:59.123456789Z',
    '2026-11-07T10:15:30-08:00',
  ])('labels a valid RFC3339 source fetch time without converting it (%s)', (value) => {
    expect(currentLocalObservedAt(value)).toBe(`Observed at ${value}`)
  })

  it.each([
    undefined,
    null,
    '',
    'not-a-time',
    '2026-02-30T10:15:30Z',
    '2026-11-07T24:00:00Z',
    '2026-11-07T10:60:30Z',
    '2026-11-07T10:15:60Z',
    '2026-11-07T10:15:30+24:00',
    '2026-11-07T10:15:30+01:60',
  ])('omits absent or invalid source times (%s)', (value) => {
    expect(currentLocalObservedAt(value)).toBeUndefined()
  })
})

describe('currentLocalUnavailableLabel', () => {
  it('qualifies unavailable, expired, and withheld states by field', () => {
    expect(currentLocalUnavailableLabel('title', 'unknown')).toBe('Video title unavailable')
    expect(currentLocalUnavailableLabel('channel_name', 'known_expired')).toBe('Source channel name unavailable — local metadata expired')
    expect(currentLocalUnavailableLabel('duration_seconds', 'withheld')).toBe('Duration unavailable for display')
  })
})
