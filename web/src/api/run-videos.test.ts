import { describe, it, expect, vi, afterEach } from 'vitest'
import { getRunVideos, getDecisionDetail } from './run-videos'

function videoListResponse(videos: unknown[], total: number): Response {
  return new Response(
    JSON.stringify({
      videos,
      pagination: { page: 1, page_size: 100, total, total_pages: total > 0 ? 1 : 0 },
    }),
    { status: 200, headers: { 'Content-Type': 'application/json' } },
  )
}

describe('getRunVideos', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('requests the run videos without query parameters by default', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(videoListResponse([], 0))

    await getRunVideos(7)

    expect(fetchSpy).toHaveBeenCalledWith('/api/runs/7/videos', expect.anything())
  })

  it('sends the outcome filter and page size as query parameters and returns the filtered total', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      videoListResponse(
        [
          {
            id: 3,
            video_id: 'v-excluded',
            channel_id: 'c1',
            processing_state: 'evaluated',
            final_outcome: 'excluded',
          },
        ],
        1,
      ),
    )

    const page = await getRunVideos(7, { outcome: 'excluded', pageSize: 100 })

    expect(fetchSpy).toHaveBeenCalledWith('/api/runs/7/videos?outcome=excluded&page_size=100', expect.anything())
    expect(page.total).toBe(1)
    expect(page.videos).toHaveLength(1)
    expect(page.videos[0].final_outcome).toBe('excluded')
  })
})

describe('getDecisionDetail', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  // Route identity on the decision surface is the stable UUID (ARS-004/
  // ARS-001): the adapter passes route_id through unchanged and keys each
  // decision by it.
  it('passes UUID route identity through as the decision key', async () => {
    const routeId = '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce'
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          video: {
            id: 1001,
            video_id: 'v1',
            channel_id: 'c1',
            processing_state: 'completed',
            final_outcome: 'accepted',
            route_decisions: [
              {
                route_id: routeId,
                route_name: 'Music Discovery',
                evaluation_order: 1,
                decision: 'matched',
                condition_audits: [],
                executed_actions: [],
              },
            ],
          },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const detail = await getDecisionDetail(1, 'v1')

    expect(detail).toBeDefined()
    const decision = detail!.route_decisions[0]
    expect(decision.route_id).toBe(routeId)
    expect(decision.id).toBe(routeId)
    expect(decision.route_name).toBe('Music Discovery')
  })
})
