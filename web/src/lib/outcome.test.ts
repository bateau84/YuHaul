import { describe, expect, it } from 'vitest'
import { deriveBadge } from './outcome'

describe('deriveBadge', () => {
  it('labels completed videos as accepted', () => {
    expect(deriveBadge('completed', 'accepted')).toEqual({ variant: 'accepted', label: 'Accepted' })
  })

  it('labels acted videos as failed', () => {
    expect(deriveBadge('acted', 'accepted')).toEqual({ variant: 'failed', label: 'Failed' })
  })

  it('labels evaluated+rejected as rejected', () => {
    expect(deriveBadge('evaluated', 'rejected')).toEqual({ variant: 'rejected', label: 'Rejected' })
  })

  it('labels deferred as deferred', () => {
    expect(deriveBadge('deferred', 'deferred')).toEqual({ variant: 'deferred', label: 'Deferred' })
  })

  it('labels not_yet_evaluated as not evaluated', () => {
    expect(deriveBadge('not_yet_evaluated', 'not_yet_evaluated')).toEqual({ variant: 'not-evaluated', label: 'Not evaluated' })
  })

  it('labels discovered/evaluating as in progress', () => {
    expect(deriveBadge('discovered', 'not_yet_evaluated')).toEqual({ variant: 'in-progress', label: 'In progress' })
    expect(deriveBadge('evaluating', 'not_yet_evaluated')).toEqual({ variant: 'in-progress', label: 'In progress' })
  })
})
