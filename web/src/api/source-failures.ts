import type { SourceFailurePage } from '../types'
import { api } from './core'

export interface SourceFailureQuery {
  /** 1..200; the server default is 50. */
  limit?: number
  /** Cursor: only rows older than this id. */
  beforeId?: number
}

function search(query: SourceFailureQuery): string {
  const params = new URLSearchParams()
  if (query.limit !== undefined) params.set('limit', String(query.limit))
  if (query.beforeId !== undefined) params.set('before_id', String(query.beforeId))
  const s = params.toString()
  return s ? `?${s}` : ''
}

function normalize(page: SourceFailurePage): SourceFailurePage {
  return { items: page.items ?? [], next_before_id: page.next_before_id ?? null }
}

// getSourceFailures reads GET /api/errors/sources: permanent failures across
// runs, newest first. Read-only; there is no write counterpart.
export async function getSourceFailures(query: SourceFailureQuery = {}): Promise<SourceFailurePage> {
  return normalize(await api<SourceFailurePage>(`/api/errors/sources${search(query)}`))
}

// getRunSourceFailures reads GET /api/runs/{id}/discovery-source-errors: the
// run's failures of every category, newest first.
export async function getRunSourceFailures(runId: number, query: SourceFailureQuery = {}): Promise<SourceFailurePage> {
  return normalize(await api<SourceFailurePage>(`/api/runs/${runId}/discovery-source-errors${search(query)}`))
}
