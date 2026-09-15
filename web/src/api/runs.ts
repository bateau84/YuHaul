import type { Route, Run, BacktestComparison } from '../types'
import { api } from './core'

export async function listRuns(): Promise<Run[]> {
  const data = await api<{ runs: Run[] }>('/api/runs')
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

export async function startRun(): Promise<{ run_id: number; status: string }> {
  return api<{ run_id: number; status: string }>('/api/runs', { method: 'POST' })
}

export async function startDryRun(draft?: { routes: Route[]; duplicate_threshold: number }): Promise<{ run_id: number; status: string }> {
  return api<{ run_id: number; status: string }>('/api/runs/dry-run', {
    method: 'POST',
    body: draft ? JSON.stringify({ draft_configuration: draft }) : undefined,
  })
}

export async function startReplay(configVersionId?: number): Promise<{ run_id: number; status: string }> {
  return api<{ run_id: number; status: string }>('/api/runs/replay', {
    method: 'POST',
    body: configVersionId !== undefined ? JSON.stringify({ config_version_id: configVersionId }) : undefined,
  })
}

export async function startBacktest(proposed: { routes: Route[]; duplicate_threshold: number }): Promise<{ run_id: number; status: string; comparison?: BacktestComparison }> {
  return api<{ run_id: number; status: string; comparison?: BacktestComparison }>('/api/backtest', {
    method: 'POST',
    body: JSON.stringify({ proposed_configuration: proposed }),
  })
}
