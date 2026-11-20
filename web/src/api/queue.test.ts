import { afterEach, describe, expect, it, vi } from 'vitest'
import { dismissError, listErrors, retryError } from './queue'

afterEach(() => vi.restoreAllMocks())

const serverEntry = {
  id: 427,
  video_id: 12,
  external_video_id: 'yt-safe-id',
  run_id: 9,
  error_category: 'api_error',
  error_message: 'quotaExceeded',
  error_message_unavailable: false,
  status: 'active',
  created_at: '2026-10-01T00:00:00Z',
  retry_count: 0,
}

describe('error queue API contract', () => {
  it('reads the server error entry fields without losing its id or safe diagnostic', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ errors: [serverEntry] })))

    const [entry] = await listErrors()

    expect(entry).toMatchObject({ id: 427, video_id: 12, external_video_id: 'yt-safe-id', error_category: 'api_error', error_message: 'quotaExceeded' })
  })

  it('uses the returned entry id for retry and dismiss endpoints', async () => {
    const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify(serverEntry)))

    await retryError(serverEntry.id)
    expect(fetch).toHaveBeenLastCalledWith('/api/errors/427/retry', expect.objectContaining({ method: 'POST' }))
    await dismissError(serverEntry.id)
    expect(fetch).toHaveBeenLastCalledWith('/api/errors/427/dismiss', expect.objectContaining({ method: 'POST' }))
  })
})
