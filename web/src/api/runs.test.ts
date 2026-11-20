import { describe, it, expect, vi, afterEach } from 'vitest'
import { startDryRun, startBacktest, getRun } from './runs'
import { getRunVideos } from './run-videos'
import type { Route, BacktestComparison } from '../types'

// One route already in the registry and one proposal-only UUID that has never
// been saved — ADR-012 accepts both in a proposal and registers the unknown
// one behind an inactive identity anchor.
const ACTIVE_ROUTE_UUID = '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce'
const PROPOSAL_ONLY_UUID = '9f2d7c31-6b8e-4a15-9c4e-2d7f1b5a6e90'

function routeFixture(overrides: Partial<Route>): Route {
  return {
    id: ACTIVE_ROUTE_UUID,
    name: 'Music Discovery',
    description: '',
    evaluation_order: 1,
    is_final: false,
    is_enabled: true,
    condition_composition: 'AND',
    conditions: [{ condition_index: 0, field: 'title', operator: 'contains', value: 'music' }],
    actions: [{ action_type: 'add_to_playlist', target_playlist_id: 'PL1', action_index: 0 }],
    exclusions: [],
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('proposal route identity (ADR-012 / ARS-004)', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  const proposalRoutes = [
    routeFixture({ id: ACTIVE_ROUTE_UUID }),
    routeFixture({ id: PROPOSAL_ONLY_UUID, name: 'New Genre Route', evaluation_order: 2 }),
  ]

  it('sends public UUID route ids in the dry-run draft configuration', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(jsonResponse({ run_id: 77, run_type: 'dry_run', status: 'running' }))

    const res = await startDryRun({ routes: proposalRoutes, duplicate_threshold: 0.5 })

    expect(res.run_id).toBe(77)
    expect(fetchSpy).toHaveBeenCalledWith('/api/runs/dry-run', expect.anything())
    const body = JSON.parse(String(fetchSpy.mock.calls[0][1]?.body))
    const sent = body.draft_configuration.routes as Route[]
    expect(sent.map((r) => r.id)).toEqual([ACTIVE_ROUTE_UUID, PROPOSAL_ONLY_UUID])
    expect(sent.map((r) => r.name)).toEqual(['Music Discovery', 'New Genre Route'])
    for (const route of sent) expect(typeof route.id).toBe('string')
    // The route identity serializes as a JSON string, never as a number
    // (ARS-004: no integer alias for a route id).
    const rawBody = String(fetchSpy.mock.calls[0][1]?.body)
    expect(rawBody).toContain(`"id":"${ACTIVE_ROUTE_UUID}"`)
    expect(rawBody).toContain(`"id":"${PROPOSAL_ONLY_UUID}"`)
    expect(rawBody).not.toMatch(/"id":\s*\d/)
  })

  it('sends public UUID route ids in the backtest proposed configuration', async () => {
    const comparison: BacktestComparison = {
      baseline_run_ids: [1, 2],
      baseline_accepted: 3,
      proposed_accepted: 5,
      new_catches: 2,
      lost_catches: 0,
      net_change: 2,
    }
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ run_id: 88, run_type: 'backtest', status: 'completed', comparison })
    )

    const res = await startBacktest({ routes: proposalRoutes, duplicate_threshold: 0.5 })

    expect(res.run_id).toBe(88)
    expect(res.comparison).toEqual(comparison)
    expect(fetchSpy).toHaveBeenCalledWith('/api/backtest', expect.anything())
    const body = JSON.parse(String(fetchSpy.mock.calls[0][1]?.body))
    const sent = body.proposed_configuration.routes as Route[]
    expect(sent.map((r) => r.id)).toEqual([ACTIVE_ROUTE_UUID, PROPOSAL_ONLY_UUID])
    expect(sent.map((r) => r.name)).toEqual(['Music Discovery', 'New Genre Route'])
    for (const route of sent) expect(typeof route.id).toBe('string')
    const rawBody = String(fetchSpy.mock.calls[0][1]?.body)
    expect(rawBody).toContain(`"id":"${ACTIVE_ROUTE_UUID}"`)
    expect(rawBody).toContain(`"id":"${PROPOSAL_ONLY_UUID}"`)
    expect(rawBody).not.toMatch(/"id":\s*\d/)
  })
})

describe('unavailable_on_youtube contract (ARS-005 §1.3)', () => {
  afterEach(() => vi.restoreAllMocks())

  it('getRun keeps videos_unavailable', async () => {
    const body = { id: 1, started_at: 'x', run_type: 'manual', status: 'completed', videos_discovered: 0, videos_accepted: 0, videos_rejected: 0, videos_excluded: 0, videos_deferred: 0, videos_failed: 0, videos_unavailable: 2, videos_not_yet_evaluated: 0, quota_cost: 0 }
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify(body)))
    const run = await getRun(1)
    expect(run?.videos_unavailable).toBe(2)
  })

  it('getRunVideos filters by the new outcome and keeps it on the row', async () => {
    const f = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(JSON.stringify({
      videos: [{ id: 1, video_id: 'v', channel_id: 'c', processing_state: 'evaluated', final_outcome: 'unavailable_on_youtube' }],
      pagination: { page: 1, page_size: 50, total: 2, total_pages: 1 },
    })))
    const page = await getRunVideos(9, { outcome: 'unavailable_on_youtube' })
    expect(f).toHaveBeenCalledWith('/api/runs/9/videos?outcome=unavailable_on_youtube', expect.anything())
    expect(page.videos[0].final_outcome).toBe('unavailable_on_youtube')
    expect(page.total).toBe(2)
  })
})
