import { describe, it, expect } from 'vitest'
import { blankRoute, shortRouteId, routeLabel } from './route'

describe('route identity helpers', () => {
  it('derives the short suffix from the first 8 hex characters of the UUID', () => {
    expect(shortRouteId('3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce')).toBe('3a554e96')
  })

  it('labels a route as name plus short suffix, and name alone when unsaved', () => {
    expect(routeLabel('Music', '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce')).toBe('Music (3a554e96)')
    expect(routeLabel('Music', '')).toBe('Music')
  })

  it('gives an unsaved route no identity until the server assigns one', () => {
    expect(blankRoute().id).toBe('')
  })
})
