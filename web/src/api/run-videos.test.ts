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

  it('retains each list route cause without inventing audits or actions', async () => {
    const cause = { status: 'available', exclusion_index: 0, config_version_id: 42 }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(videoListResponse([{
      id: 1, video_id: 'v', channel_id: 'c', processing_state: 'evaluated', final_outcome: 'rejected',
      route_decisions: [
        { route_id: 'r1', route_name: 'A', route_name_source: 'run_snapshot', decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: cause },
        { route_id: 'r2', route_name: 'B', route_name_source: 'current_local', decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: null },
      ],
    }], 1))
    const [first, second] = (await getRunVideos(1)).videos[0].route_decisions!
    expect(first.causal_exclusion).toEqual(cause)
    expect(first.route_name_source).toBe('run_snapshot')
    expect(first).not.toHaveProperty('actions')
    expect(second.route_id).toBe('r2')
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

  it('preserves current/local metadata fields from a list response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(videoListResponse([{
      id: 1, video_id: 'v1', channel_id: 'c1', processing_state: 'evaluated', final_outcome: 'accepted',
      current_local_metadata: { attribution: 'current_local_not_necessarily_evaluated', source_fetched_at: '2026-11-07T10:15:30Z', title: 'Local title', title_availability: 'current_local', channel_name: null, channel_name_availability: 'withheld', duration_seconds: 0, duration_availability: 'current_local' },
      metadata_context_status: 'available',
    }], 1))
    const result = await getRunVideos(1)
    expect(result.videos[0].current_local_metadata?.title).toBe('Local title')
    expect(result.videos[0].current_local_metadata?.duration_seconds).toBe(0)
    expect(result.videos[0].current_local_metadata?.channel_name_availability).toBe('withheld')
    expect(result.videos[0].current_local_metadata?.source_fetched_at).toBe('2026-11-07T10:15:30Z')
    expect(result.videos[0].metadata_context_status).toBe('available')
  })

  it('preserves run-scoped unresolved mutation guards from a list response', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(videoListResponse([{
      id: 8, video_id: 'v-guard', channel_id: 'c1', processing_state: 'failed', final_outcome: 'failed',
      unresolved_mutations: [{ action_type: 'add_to_playlist', target_playlist_id: 'PL1', prepared_at: '2026-11-07T10:00:00Z', application_state: 'unknown' }],
    }], 1))
    const result = await getRunVideos(12)
    expect(result.videos[0].unresolved_mutations).toEqual([{
      action_type: 'add_to_playlist', target_playlist_id: 'PL1', prepared_at: '2026-11-07T10:00:00Z', application_state: 'unknown',
    }])
  })

  it('requests later pages through the page parameter and returns the page bounds', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          videos: [],
          pagination: { page: 4, page_size: 100, total: 350, total_pages: 4 },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const result = await getRunVideos(7, { pageSize: 100, page: 4 })

    expect(fetchSpy).toHaveBeenCalledWith('/api/runs/7/videos?page_size=100&page=4', expect.anything())
    expect(result.total).toBe(350)
    expect(result.totalPages).toBe(4)
    expect(result.videos).toHaveLength(0)
  })

  it('omits the page parameter for page 1, matching the server default', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(videoListResponse([], 0))

    await getRunVideos(7, { pageSize: 100, page: 1 })

    expect(fetchSpy).toHaveBeenCalledWith('/api/runs/7/videos?page_size=100', expect.anything())
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

  // The action detail must carry every diagnostic field the server projects
  // (executedActionDetail): outcome reason, safe/unavailable error state, and
  // the attempt timestamp — otherwise Decision Detail cannot present run-
  // scoped action evidence (ARD-001 / Task 2.2).
  it('passes through action outcome reason, error availability, and attempt timestamp', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(
        JSON.stringify({
          video: {
            id: 1001,
            video_id: 'v1',
            channel_id: 'c1',
            processing_state: 'completed',
            final_outcome: 'accepted',
            error_message_unavailable: true,
            route_decisions: [
              {
                route_id: '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce',
                route_name: 'Music Discovery',
                evaluation_order: 1,
                decision: 'matched',
                condition_audits: [],
                executed_actions: [
                  {
                    action_type: 'add_to_playlist',
                    target_playlist_id: 'PLmusic',
                    outcome: 'not_attempted',
                    outcome_reason: 'already_completed',
                  },
                  {
                    action_type: 'add_to_playlist',
                    target_playlist_id: 'PLother',
                    outcome: 'failed',
                    error_message_unavailable: true,
                    executed_at: '2026-10-27T09:30:00Z',
                  },
                ],
              },
            ],
          },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } },
      ),
    )

    const detail = await getDecisionDetail(1, 'v1')

    expect(detail!.video.error_message_unavailable).toBe(true)
    const [suppressed, failed] = detail!.route_decisions[0].actions
    expect(suppressed.outcome).toBe('not_attempted')
    expect(suppressed.outcome_reason).toBe('already_completed')
    expect(suppressed.executed_at).toBeUndefined()
    expect(suppressed.error_message).toBeUndefined()
    expect(failed.outcome).toBe('failed')
    expect(failed.error_message_unavailable).toBe(true)
    expect(failed.executed_at).toBe('2026-10-27T09:30:00Z')
    expect(failed.error_message).toBeUndefined()
  })

  it('passes through unresolved mutation guards without manufacturing an action attempt', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({ video: {
      id: 8, video_id: 'v-guard', channel_id: 'c1', processing_state: 'failed', final_outcome: 'failed',
      unresolved_mutations: [{ action_type: 'add_to_playlist', target_playlist_id: 'PL1', prepared_at: '2026-11-07T10:00:00Z', application_state: 'unknown' }],
      route_decisions: [],
    } }), { status: 200, headers: { 'Content-Type': 'application/json' } }))
    const detail = await getDecisionDetail(12, 'v-guard')
    expect(detail?.video.unresolved_mutations?.[0].application_state).toBe('unknown')
    expect(detail?.route_decisions).toEqual([])
  })
})
