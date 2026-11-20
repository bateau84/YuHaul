import { describe, it, expect, vi, afterEach } from 'vitest'
import { stopRun, discardRun, endRun, stopRefusalCode, discardRefusalCode, endRefusalCode } from './runs'
import { ApiError } from './core'
import { isStopping, canOfferStop } from '../types'

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

async function failure(p: Promise<unknown>): Promise<unknown> {
  return p.then(() => undefined, (e: unknown) => e)
}

// Stop and Discard client contract (run-initiation ARS-002 §3, ARS-003 §4).
describe('stopRun', () => {
  afterEach(() => vi.restoreAllMocks())

  it.each(['accepted', 'already_stopping'] as const)('posts without a body and returns %s', async (stop) => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ run_id: 41, status: 'running', stop }, 202))

    expect(await stopRun(41)).toEqual({ run_id: 41, status: 'running', stop })
    expect(spy).toHaveBeenCalledWith('/api/runs/41/stop', expect.objectContaining({ method: 'POST' }))
    expect((spy.mock.calls[0][1] as RequestInit).body).toBeUndefined()
  })

  it.each([
    [400, { error: 'invalid run id', code: 'invalid_run_id' }],
    [503, { error: 'server is shutting down', code: 'shutting_down', run_id: 41 }],
    [404, { error: 'run 41 does not exist', code: 'run_not_found', run_id: 41 }],
    [409, { error: 'run 41 has already ended', code: 'run_ended', run_id: 41, status: 'stopped' }],
    [409, { error: 'run 41 is not running', code: 'run_not_active', run_id: 41, reason: 'ending' }],
    [500, { error: 'the stop was not recorded; run 41 continues', code: 'stop_failed', run_id: 41 }],
  ])('maps %i %j to its typed code', async (status, body) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(body, status))

    const err = await failure(stopRun(41))

    expect(err).toBeInstanceOf(ApiError)
    expect(stopRefusalCode(err)).toBe(body.code)
    expect((err as ApiError).status).toBe(status)
    expect((err as ApiError).message).toBe(body.error)
  })

  it('keeps run_not_active reason', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'x', code: 'run_not_active', run_id: 41, reason: 'not_executing' }, 409))
    expect(((await failure(stopRun(41))) as ApiError).reason).toBe('not_executing')
  })

  it('gives no code for a network failure or unknown code', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('network'))
    expect(stopRefusalCode(await failure(stopRun(41)))).toBeUndefined()
    vi.restoreAllMocks()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'x', code: 'weird' }, 409))
    expect(stopRefusalCode(await failure(stopRun(41)))).toBeUndefined()
  })
})

describe('discardRun', () => {
  afterEach(() => vi.restoreAllMocks())

  it('posts to the run endpoint and returns the cause', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ run_id: 30, status: 'failed', outcome: 'discarded', cause: 'snapshot_unavailable' }, 200),
    )

    expect(await discardRun(30)).toEqual({ run_id: 30, status: 'failed', outcome: 'discarded', cause: 'snapshot_unavailable' })
    expect(spy).toHaveBeenCalledWith('/api/runs/30/discard', expect.objectContaining({ method: 'POST' }))
  })

  it.each([
    [400, { error: 'invalid run id', code: 'invalid_run_id' }],
    [503, { error: 'server is shutting down', code: 'shutting_down', run_id: 30 }],
    [503, { error: 'startup recovery', code: 'startup_recovery_active', run_id: 30 }],
    [409, { error: 'a production run is already active', code: 'production_run_active', run_id: 30 }],
    [404, { error: 'run 30 does not exist', code: 'run_not_found', run_id: 30 }],
    [409, { error: 'run 30 has already ended', code: 'run_ended', run_id: 30, status: 'failed' }],
    [409, { error: 'not the run', code: 'run_not_discardable', run_id: 30, reason: 'not_oldest' }],
    [500, { error: 'did not take effect', code: 'discard_failed', run_id: 30 }],
    [500, { error: 'could not confirm', code: 'discard_outcome_unknown', run_id: 30 }],
  ])('maps %i %j to its typed code', async (status, body) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(body, status))

    const err = await failure(discardRun(30))

    expect(discardRefusalCode(err)).toBe(body.code)
    expect((err as ApiError).status).toBe(status)
    expect((err as ApiError).runId).toBe('run_id' in body ? 30 : undefined)
  })

  it('keeps run_not_discardable reason', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'x', code: 'run_not_discardable', run_id: 30, reason: 'resumable' }, 409))
    expect(((await failure(discardRun(30))) as ApiError).reason).toBe('resumable')
  })

  it('gives no code for a network failure', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('network'))
    expect(discardRefusalCode(await failure(discardRun(30)))).toBeUndefined()
  })
})

describe('isStopping', () => {
  it('is true only for a running run with a stop marker', () => {
    expect(isStopping({ status: 'running', stop_requested_at: '2026-11-10T12:00:03Z' })).toBe(true)
    expect(isStopping({ status: 'running', stop_requested_at: null })).toBe(false)
    expect(isStopping({ status: 'running' })).toBe(false)
    expect(isStopping({ status: 'stopped', stop_requested_at: '2026-11-10T12:00:03Z' })).toBe(false)
  })
})

// "Mark as ended" client contract (run-initiation ARS-004 §3) and the shared
// Stop predicate.
describe('endRun', () => {
  afterEach(() => vi.restoreAllMocks())

  it('posts without a body and returns the ended result', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ run_id: 14, status: 'failed', outcome: 'ended' }, 200))

    expect(await endRun(14)).toEqual({ run_id: 14, status: 'failed', outcome: 'ended' })
    expect(spy).toHaveBeenCalledWith('/api/runs/14/end', expect.objectContaining({ method: 'POST' }))
    expect((spy.mock.calls[0][1] as RequestInit).body).toBeUndefined()
  })

  it.each([
    [400, { error: 'invalid run id', code: 'invalid_run_id' }],
    [404, { error: 'run 14 does not exist', code: 'run_not_found', run_id: 14 }],
    [409, { error: 'run 14 has already ended', code: 'run_ended', run_id: 14, status: 'failed' }],
    [409, { error: 'x', code: 'run_not_endable', run_id: 14, reason: 'not_production', run_type: 'replay' }],
    [409, { error: 'x', code: 'run_not_endable', run_id: 14, reason: 'not_legacy' }],
    [409, { error: 'x', code: 'run_not_endable', run_id: 14, reason: 'executing' }],
    [409, { error: 'x', code: 'production_run_active', run_id: 14 }],
    [503, { error: 'x', code: 'startup_recovery_active', run_id: 14 }],
    [503, { error: 'x', code: 'shutting_down', run_id: 14 }],
    [500, { error: 'x', code: 'end_failed', run_id: 14, status: 'interrupted' }],
    [500, { error: 'x', code: 'end_outcome_unknown', run_id: 14 }],
  ])('maps %i %j to its typed code', async (status, body) => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(body, status))

    const err = (await failure(endRun(14))) as ApiError

    expect(err).toBeInstanceOf(ApiError)
    expect(endRefusalCode(err)).toBe(body.code)
    expect(err.status).toBe(status)
    expect(err.reason).toBe((body as { reason?: string }).reason)
    expect(err.runType).toBe((body as { run_type?: string }).run_type)
    expect(err.savedStatus).toBe((body as { status?: string }).status)
  })

  it('gives no code for a network failure or an unknown code', async () => {
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(new TypeError('network'))
    expect(endRefusalCode(await failure(endRun(14)))).toBeUndefined()
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'x', code: 'surprise' }, 409))
    expect(endRefusalCode(await failure(endRun(14)))).toBeUndefined()
  })
})

describe('canOfferStop', () => {
  it('is true only for a run saved as running that the server is executing', () => {
    expect(canOfferStop({ status: 'running', executing: true })).toBe(true)
    // run 14: saved as running, nothing executes it
    expect(canOfferStop({ status: 'running', executing: false })).toBe(false)
    // an older server that does not report executing never offers Stop
    expect(canOfferStop({ status: 'running' })).toBe(false)
    for (const status of ['interrupted', 'completed', 'failed', 'stopped'] as const) {
      expect(canOfferStop({ status, executing: true })).toBe(false)
    }
  })
})
