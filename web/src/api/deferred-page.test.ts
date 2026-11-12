import { afterEach, describe, expect, it, vi } from 'vitest'
import { getDeferredPage } from './deferred-page'
import { ApiError } from './core'
import { deferredReasons } from '../types'
import { deferredReasonLabels } from '../lib/outcome'

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => vi.restoreAllMocks())

const page = {
  deferred: [{ id: 51, video_id: 7, run_id: 3, deferral_reason: 'requeued_by_user', status: 'pending', created_at: '2026-11-11T00:00:00Z', retry_count: 0 }],
  total: 51,
  queue_total: 70000,
  by_reason: { quota_exhausted: 51, requeued_by_user: 69949 },
  page: { limit: 50, number: 2, total_pages: 2, first_position: 51, last_position: 51, next_after_id: null, prev_before_id: 51 },
}

describe('getDeferredPage (ARS-005 §7.2)', () => {
  it('requests the bare path with no parameters and keeps the whole page shape', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockResolvedValue(json(page))
    const res = await getDeferredPage()
    expect(f).toHaveBeenCalledWith('/api/deferred', expect.anything())
    expect(res.total).toBe(51)
    expect(res.queue_total).toBe(70000)
    expect(res.by_reason.requeued_by_user).toBe(69949)
    expect(res.page).toEqual(page.page)
    expect(res.deferred[0].deferral_reason).toBe('requeued_by_user')
  })

  it('sends include, reason, limit and one cursor or page jump as the server names them', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => json(page))
    await getDeferredPage({ include: 'all', reason: 'quota_exhausted', limit: 200, afterId: 50 })
    expect(f).toHaveBeenLastCalledWith('/api/deferred?include=all&reason=quota_exhausted&limit=200&after_id=50', expect.anything())
    await getDeferredPage({ beforeId: 51 })
    expect(f).toHaveBeenLastCalledWith('/api/deferred?before_id=51', expect.anything())
    await getDeferredPage({ page: 'last' })
    expect(f).toHaveBeenLastCalledWith('/api/deferred?page=last', expect.anything())
    await getDeferredPage({ page: 700 })
    expect(f).toHaveBeenLastCalledWith('/api/deferred?page=700', expect.anything())
  })

  it('normalizes a past-the-end page with null/empty members', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ deferred: null, total: 10, queue_total: 10, by_reason: null, page: { ...page.page, number: 9, first_position: null, last_position: null } }))
    const res = await getDeferredPage({ page: 9 })
    expect(res.deferred).toEqual([])
    expect(res.by_reason).toEqual({})
    expect(res.page.first_position).toBeNull()
  })

  it.each([
    [400, 'invalid page', 'invalid_page'],
    [400, 'invalid reason', 'invalid_reason'],
  ])('surfaces status %i and code %s', async (status, error, code) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ error, code }, status))
    const err = await getDeferredPage({ limit: 0 }).then(() => undefined, (e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(status)
    expect((err as ApiError).code).toBe(code)
  })

  it('has exactly the six stored reasons, each labelled, with no Other', () => {
    expect([...deferredReasons].sort()).toEqual(
      ['api_rate_limited', 'authorization_pending', 'discarded_by_user', 'quota_exhausted', 'requeued_by_user', 'stopped_by_user'],
    )
    for (const r of deferredReasons) expect(deferredReasonLabels[r]).toBeTruthy()
    expect(Object.keys(deferredReasonLabels)).toHaveLength(6)
  })
})
