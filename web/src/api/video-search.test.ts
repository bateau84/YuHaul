import { afterEach, describe, expect, it, vi } from 'vitest'
import { getVideo, searchVideos } from './video-search'
import { ApiError } from './core'

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

afterEach(() => vi.restoreAllMocks())

const summary = {
  video_id: 'dQw4w9WgXcQ',
  record: 'known',
  title: { text: 'Lofi beats', availability: 'current_local' },
  channel: { name: null, name_availability: 'expired', channel_id: 'UC1' },
  latest_outcome: { state: 'committed', outcome: 'unavailable_on_youtube', run_id: 58, committed_at: '2026-11-10T00:00:00Z' },
  queue_state: { state: 'none' },
}

describe('video search client (video-search ARS-001)', () => {
  it('sends the text as typed, URL-encoded', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ kind: 'too_short' }))
    const res = await searchVideos('  lo&fi % ')
    expect(f).toHaveBeenCalledWith('/api/videos/search?q=++lo%26fi+%25+', expect.anything())
    expect(res.kind).toBe('too_short')
  })

  it('reads a results page with total, positions and cursors', async () => {
    const results = { items: [summary], total: 130, page_size: 50, number: 2, total_pages: 3, first_position: 51, last_position: 51, next_cursor: 'n', prev_cursor: 'p' }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ kind: 'results', results }))
    const res = await searchVideos('lofi')
    expect(res.kind).toBe('results')
    if (res.kind !== 'results') throw new Error('unreachable')
    expect(res.results.total).toBe(130)
    expect(res.results.first_position).toBe(51)
    expect(res.results.items[0].latest_outcome).toMatchObject({ state: 'committed', outcome: 'unavailable_on_youtube' })
  })

  it('reads the video and id_not_found kinds', async () => {
    const f = vi.spyOn(globalThis, 'fetch')
    f.mockResolvedValueOnce(json({ kind: 'video', video_id: 'dQw4w9WgXcQ', from_link: true }))
    expect(await searchVideos('https://youtu.be/dQw4w9WgXcQ')).toEqual({ kind: 'video', video_id: 'dQw4w9WgXcQ', from_link: true })
    f.mockResolvedValueOnce(json({ kind: 'id_not_found', video_id: 'dQw4w9WgXcQ', from_link: true }))
    expect(await searchVideos('https://youtu.be/dQw4w9WgXcQ')).toMatchObject({ kind: 'id_not_found', from_link: true })
  })

  it('sends a cursor or a page jump, never both implicitly', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => json({ kind: 'empty' }))
    await searchVideos('lofi', { cursor: 'abc' })
    expect(f).toHaveBeenLastCalledWith('/api/videos/search?q=lofi&cursor=abc', expect.anything())
    await searchVideos('lofi', { page: 3 })
    expect(f).toHaveBeenLastCalledWith('/api/videos/search?q=lofi&page=3', expect.anything())
  })

  it.each(['invalid_cursor', 'invalid_page', 'search_failed'])('surfaces status and the closed code %s', async (code) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ error: code }, code === 'search_failed' ? 500 : 400))
    const err = await searchVideos('lofi', { page: 0 }).then(() => undefined, (e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).message).toBe(code)
    expect((err as ApiError).status).toBe(code === 'search_failed' ? 500 : 400)
  })

  it('reads the video view with queue state and run entries', async () => {
    const view = { ...summary, queue_state: { state: 'deferred_claimed', reason: 'requeued_by_user', claiming_run_id: 60 }, runs: [{ run_id: 58, run_type: 'manual', mode: 'production', started_at: '2026-11-10T08:00:00Z', initiated_by: null, run_status: 'completed', outcome: null, decision_detail_path: '/runs/58/video/dQw4w9WgXcQ' }] }
    const f = vi.spyOn(globalThis, 'fetch').mockResolvedValue(json(view))
    const res = await getVideo('dQw4w9WgXcQ')
    expect(f).toHaveBeenCalledWith('/api/videos/dQw4w9WgXcQ', expect.anything())
    expect(res.queue_state).toEqual({ state: 'deferred_claimed', reason: 'requeued_by_user', claiming_run_id: 60 })
    expect(res.runs[0].outcome).toBeNull()
  })

  it.each([[400, 'invalid_video_id'], [404, 'video_not_found'], [500, 'video_view_failed']])('surfaces %i %s for the view', async (status, code) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(json({ error: code }, status))
    const err = await getVideo('x').then(() => undefined, (e: unknown) => e)
    expect((err as ApiError).status).toBe(status)
    expect((err as ApiError).message).toBe(code)
  })
})
