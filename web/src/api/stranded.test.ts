import { afterEach, describe, expect, it, vi } from 'vitest'
import { getStrandedPreview, requeueStranded } from './stranded'
import { ApiError } from './core'
import { requeueRefusalCodes } from '../types'

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => vi.restoreAllMocks())

const report = {
  requeue: 70000,
  already_queued: { videos: 1180, in_errors_queue: 2 },
  removed_by_user: 3,
  blocked: { videos: 41, by_reason: { uncertain_addition: 2, unresolved_send_guard: 4, ambiguous_legacy_evidence: 35 }, items: [{ video_id: 812, youtube_video_id: 'dQw4w9WgXcQ', reasons: [{ reason: 'ambiguous_legacy_evidence', run_id: 14 }] }] },
  not_ended: { videos: 12, runs: [{ run_id: 52, videos: 12 }] },
  no_origin_run: 0,
  retained_without_queue_item: { videos: 150, removed_by_user: 40 },
}

describe('stranded videos client (ARS-005 §8)', () => {
  it('reads every preview field', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ available: true, unavailable_reason: null, ...report }))
    const p = await getStrandedPreview()
    expect(f).toHaveBeenCalledWith('/api/stranded-videos', expect.anything())
    expect(p).toEqual({ available: true, unavailable_reason: null, ...report })
  })

  it('carries an unavailable reason', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ available: false, unavailable_reason: 'production_run_active', ...report }))
    expect((await getStrandedPreview()).unavailable_reason).toBe('production_run_active')
  })

  it('confirms with the previewed count and reads the acknowledgement', async () => {
    const ack = { outcome: 'requeued', requeued: 69998, skipped_concurrently: 2, expected_requeue: 70000, differs_from_preview: true, ...report }
    const f = vi.spyOn(globalThis, 'fetch').mockResolvedValue(json(ack))
    const res = await requeueStranded(70000)
    expect(f).toHaveBeenCalledWith('/api/stranded-videos/requeue', expect.objectContaining({ method: 'POST', body: '{"expected_requeue":70000}' }))
    expect(res.requeued).toBe(69998)
    expect(res.skipped_concurrently).toBe(2)
    expect(res.differs_from_preview).toBe(true)
    expect(res.outcome).toBe('requeued')
  })

  it('sends an empty body when no count is expected', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ outcome: 'nothing_to_requeue', requeued: 0, skipped_concurrently: 0, expected_requeue: null, differs_from_preview: false, ...report }))
    const res = await requeueStranded()
    expect(f.mock.calls[0][1]?.body).toBeUndefined()
    expect(res.outcome).toBe('nothing_to_requeue')
    expect(res.expected_requeue).toBeNull()
  })

  it.each([
    [400, 'invalid_body'],
    [503, 'shutting_down'],
    [503, 'startup_recovery_active'],
    [409, 'production_run_active'],
    [500, 'requeue_failed'],
    [500, 'requeue_outcome_unknown'],
  ])('surfaces status %i with code %s', async (status, code) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ error: 'safe text', code }, status))
    const err = await requeueStranded(1).then(() => undefined, (e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(status)
    expect((err as ApiError).code).toBe(code)
    expect(requeueRefusalCodes).toContain(code)
  })

  it('surfaces a preview read failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ error: 'the stranded videos could not be counted', code: 'preview_failed' }, 500))
    const err = await getStrandedPreview().then(() => undefined, (e: unknown) => e)
    expect((err as ApiError).code).toBe('preview_failed')
  })
})
