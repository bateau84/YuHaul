import type { DeferredPage, DeferredReason } from '../types'
import { api } from './core'

export interface DeferredPageQuery {
  include?: 'pending' | 'all'
  /** One of the six stored reasons; applied by the server. */
  reason?: DeferredReason
  /** 1..200; the server default is 50. */
  limit?: number
  /** Keyset cursors and page jumps are mutually exclusive (ARS-005 §7.2). */
  afterId?: number
  beforeId?: number
  /** A 1-based page number or 'last'. */
  page?: number | 'last'
}

// getDeferredPage fetches one page of GET /api/deferred. Invalid combinations
// are refused by the server as 400 invalid_page / invalid_reason (ApiError.code).
export async function getDeferredPage(query: DeferredPageQuery = {}): Promise<DeferredPage> {
  const params = new URLSearchParams()
  if (query.include) params.set('include', query.include)
  if (query.reason) params.set('reason', query.reason)
  if (query.limit !== undefined) params.set('limit', String(query.limit))
  if (query.afterId !== undefined) params.set('after_id', String(query.afterId))
  if (query.beforeId !== undefined) params.set('before_id', String(query.beforeId))
  if (query.page !== undefined) params.set('page', String(query.page))
  const search = params.toString()
  const data = await api<DeferredPage>(`/api/deferred${search ? `?${search}` : ''}`)
  return { ...data, deferred: data.deferred ?? [], by_reason: data.by_reason ?? {} }
}
