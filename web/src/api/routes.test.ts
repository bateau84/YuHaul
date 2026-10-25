import { describe, it, expect, vi, afterEach } from 'vitest'
import { createRoute, updateRoute, reorderRoutes, deleteRoute, getRoute } from './routes'
import { blankRoute } from '../lib/route'
import { ApiError } from './core'

const ROUTE_UUID = '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('route API identity contract', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('omits the route id from create requests (ARS-004: the server assigns the UUID)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ ...blankRoute(2), id: ROUTE_UUID, name: 'Music' }, 201)
    )

    await createRoute({ ...blankRoute(2), name: 'Music' })

    expect(fetchSpy).toHaveBeenCalledWith('/api/routes', expect.anything())
    const body = JSON.parse(String(fetchSpy.mock.calls[0][1]?.body))
    expect(body.id).toBeUndefined()
    expect(body.created_at).toBeUndefined()
    expect(body.updated_at).toBeUndefined()
    expect(body.name).toBe('Music')
  })

  it('addresses routes by UUID path param on get, update, and delete', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ ...blankRoute(1), id: ROUTE_UUID })
    )

    await getRoute(ROUTE_UUID)
    expect(fetchSpy).toHaveBeenCalledWith(`/api/routes/${ROUTE_UUID}`, expect.anything())

    fetchSpy.mockResolvedValue(jsonResponse({ ...blankRoute(1), id: ROUTE_UUID }))
    await updateRoute(ROUTE_UUID, { ...blankRoute(1), name: 'Renamed' })
    expect(fetchSpy).toHaveBeenLastCalledWith(`/api/routes/${ROUTE_UUID}`, expect.anything())

    fetchSpy.mockResolvedValue(new Response(null, { status: 204 }))
    await deleteRoute(ROUTE_UUID)
    expect(fetchSpy).toHaveBeenLastCalledWith(`/api/routes/${ROUTE_UUID}`, expect.anything())
  })

  it('maps an unknown UUID to undefined (404) and keeps other failures as errors', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ error: 'route not found' }, 404)
    )

    await expect(getRoute(ROUTE_UUID)).resolves.toBeUndefined()

    fetchSpy.mockResolvedValue(jsonResponse({ error: 'invalid route id' }, 400))
    await expect(getRoute('not-a-uuid')).rejects.toBeInstanceOf(ApiError)
  })

  it('sends reorder requests as UUID route_ids in requested order', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ ok: true }))
    const ids = [ROUTE_UUID, '4c7c3b7e-f09e-4bf4-9b55-aec0cf479b51']

    await reorderRoutes(ids)

    expect(fetchSpy).toHaveBeenCalledWith('/api/routes/reorder', expect.anything())
    const body = JSON.parse(String(fetchSpy.mock.calls[0][1]?.body))
    expect(body.route_ids).toEqual(ids)
  })
})
