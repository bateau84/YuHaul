import { describe, it, expect, vi, afterEach } from 'vitest'
import { startRun } from './runs'
import { ApiError } from './core'

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

// Run Now acknowledgement and refusals (run-initiation ARS-001 §3.1, §3.5).
describe('startRun', () => {
  afterEach(() => vi.restoreAllMocks())

  it.each(['started', 'resumed'] as const)('returns the run to follow and how it was admitted (%s)', async (admission) => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ run_id: 30, status: 'running', admission }, 202))

    const res = await startRun()

    expect(res).toEqual({ run_id: 30, status: 'running', admission })
    expect(fetchSpy).toHaveBeenCalledWith('/api/runs', expect.objectContaining({ method: 'POST' }))
  })

  it('exposes code, run id and reason of an unusable resume candidate', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ error: 'run 30 stopped at its time limit but cannot be resumed safely; no new run was started', code: 'resume_unavailable', run_id: 30, reason: 'snapshot_unavailable' }, 409),
    )

    const err = await startRun().then(() => undefined, (e: unknown) => e)

    expect(err).toBeInstanceOf(ApiError)
    const api = err as ApiError
    expect(api.status).toBe(409)
    expect(api.code).toBe('resume_unavailable')
    expect(api.runId).toBe(30)
    expect(api.reason).toBe('snapshot_unavailable')
    expect(api.message).toContain('cannot be resumed safely')
  })

  it('exposes the code of a refusal while another run is active', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'a production run is already active', code: 'production_run_active' }, 409))

    const err = (await startRun().then(() => undefined, (e: unknown) => e)) as ApiError

    expect(err.status).toBe(409)
    expect(err.code).toBe('production_run_active')
    expect(err.runId).toBeUndefined()
    expect(err.reason).toBeUndefined()
  })

  it('keeps plain errors free of refusal fields', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'failed to start run' }, 500))

    const err = (await startRun().then(() => undefined, (e: unknown) => e)) as ApiError

    expect(err.status).toBe(500)
    expect(err.code).toBeUndefined()
  })
})
