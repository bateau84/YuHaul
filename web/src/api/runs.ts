import type { Route, Run, BacktestComparison, DiscardCause, RunStatus } from '../types'
import { api, ApiError } from './core'

export async function listRuns(): Promise<Run[]> {
  const data = await api<{ runs: Run[] }>('/api/runs')
  // A missing or non-array collection violates the API contract (ARS-001);
  // surface it as a failure instead of normalizing it to an empty list.
  if (!data || !Array.isArray(data.runs)) {
    throw new Error('Invalid response from /api/runs: runs field missing or not an array')
  }
  return data.runs.sort((a, b) => +new Date(b.started_at) - +new Date(a.started_at))
}

export async function getRun(id: number): Promise<Run | undefined> {
  try {
    return await api<Run>(`/api/runs/${id}`)
  } catch (err) {
    if (err instanceof Error && err.message.includes('not found')) {
      return undefined
    }
    throw err
  }
}

// Run Now either resumes the oldest run that stopped at its time limit
// ("resumed") or starts a new one ("started"). Progress follows run_id in both
// cases. A refusal is a 409 ApiError carrying code (production_run_active |
// resume_unavailable) and, for resume_unavailable, runId and reason.
export interface StartRunResult {
  run_id: number
  status: string
  // Optional on purpose: a 202 without a known admission is accepted without
  // claiming it was new or resumed.
  admission?: 'started' | 'resumed'
}

export async function startRun(): Promise<StartRunResult> {
  return api<StartRunResult>('/api/runs', { method: 'POST' })
}

// Stop and Discard refusals are ApiErrors whose code is one of these closed
// values (ARS-002 §3, ARS-003 §4.1). reason accompanies run_not_active
// (not_executing | ending) and run_not_discardable
// (resumable | not_oldest | not_eligible); runId is always set for them.
export type StopRefusalCode =
  | 'invalid_run_id'
  | 'shutting_down'
  | 'run_not_found'
  | 'run_ended'
  | 'run_not_active'
  | 'stop_failed'

export type DiscardRefusalCode =
  | 'invalid_run_id'
  | 'shutting_down'
  | 'startup_recovery_active'
  | 'production_run_active'
  | 'run_not_found'
  | 'run_ended'
  | 'run_not_discardable'
  | 'discard_failed'
  | 'discard_outcome_unknown'

const STOP_REFUSAL_CODES: readonly string[] = ['invalid_run_id', 'shutting_down', 'run_not_found', 'run_ended', 'run_not_active', 'stop_failed']
const DISCARD_REFUSAL_CODES: readonly string[] = [
  'invalid_run_id', 'shutting_down', 'startup_recovery_active', 'production_run_active', 'run_not_found',
  'run_ended', 'run_not_discardable', 'discard_failed', 'discard_outcome_unknown',
]

// Narrow an error from stopRun/discardRun to its closed refusal code. Returns
// undefined for network failures and any error without a known code.
export function stopRefusalCode(err: unknown): StopRefusalCode | undefined {
  return err instanceof ApiError && err.code && STOP_REFUSAL_CODES.includes(err.code) ? (err.code as StopRefusalCode) : undefined
}

export function discardRefusalCode(err: unknown): DiscardRefusalCode | undefined {
  return err instanceof ApiError && err.code && DISCARD_REFUSAL_CODES.includes(err.code) ? (err.code as DiscardRefusalCode) : undefined
}

export interface StopRunResult {
  run_id: number
  status: 'running'
  // already_stopping is a success: a stop was recorded earlier.
  stop: 'accepted' | 'already_stopping'
}

export async function stopRun(id: number): Promise<StopRunResult> {
  return api<StopRunResult>(`/api/runs/${id}/stop`, { method: 'POST' })
}

export interface DiscardRunResult {
  run_id: number
  status: Extract<RunStatus, 'failed'>
  outcome: 'discarded'
  cause: DiscardCause
}

export async function discardRun(id: number): Promise<DiscardRunResult> {
  return api<DiscardRunResult>(`/api/runs/${id}/discard`, { method: 'POST' })
}

export async function startDryRun(draft?: { routes: Route[]; duplicate_threshold: number }): Promise<{ run_id: number; status: string }> {
  return api<{ run_id: number; status: string }>('/api/runs/dry-run', {
    method: 'POST',
    body: draft ? JSON.stringify({ draft_configuration: draft }) : undefined,
  })
}

// Calendar-day strings exactly as a date input holds them (YYYY-MM-DD). They
// are sent unchanged and never pass through Date, so the browser's time zone
// cannot move a day (run-initiation ADR-002). An empty string means unchosen.
export interface PublishedDays {
  from?: string
  to?: string
}

function publishedDayFields(days?: PublishedDays): { published_from?: string; published_to?: string } {
  const out: { published_from?: string; published_to?: string } = {}
  if (days?.from) out.published_from = days.from
  if (days?.to) out.published_to = days.to
  return out
}

export async function startReplay(configVersionId?: number, days?: PublishedDays): Promise<{ run_id: number; status: string }> {
  const body = {
    ...(configVersionId !== undefined ? { config_version_id: configVersionId } : {}),
    ...publishedDayFields(days),
  }
  return api<{ run_id: number; status: string }>('/api/runs/replay', {
    method: 'POST',
    body: Object.keys(body).length > 0 ? JSON.stringify(body) : undefined,
  })
}

export async function startBacktest(proposed: { routes: Route[]; duplicate_threshold: number }, days?: PublishedDays): Promise<{ run_id: number; status: string; comparison?: BacktestComparison }> {
  return api<{ run_id: number; status: string; comparison?: BacktestComparison }>('/api/backtest', {
    method: 'POST',
    body: JSON.stringify({ proposed_configuration: proposed, ...publishedDayFields(days) }),
  })
}
