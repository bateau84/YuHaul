import { describe, it, expect } from 'vitest'
import { runEnding } from './run-ending'

const END = 'diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user'
const DISCARD = 'diag:v1 phase=unknown operation=discard_run code=snapshot_unavailable'

describe('runEnding', () => {
  it('tells a user end from a discard and from a self-failure', () => {
    expect(runEnding({ status: 'failed', interrupt_reason: END })).toBe('ended_by_user')
    expect(runEnding({ status: 'failed', interrupt_reason: DISCARD })).toBe('discarded_by_user')
    expect(runEnding({ status: 'failed', interrupt_reason: 'diag:v1 phase=discovery operation=discover code=timeout' })).toBeNull()
    expect(runEnding({ status: 'failed', interrupt_reason: null })).toBeNull()
    expect(runEnding({ status: 'failed' })).toBeNull()
  })

  it('never reports an ending for a run that is not failed', () => {
    for (const status of ['running', 'interrupted', 'completed', 'stopped'] as const) {
      expect(runEnding({ status, interrupt_reason: END })).toBeNull()
    }
  })

  it('does not trust message text', () => {
    expect(runEnding({ status: 'failed', interrupt_reason: 'ended by user (legacy run)' })).toBeNull()
  })
})
