import type { ErrorEntry, DeferredItem } from '../types'
import { api } from './core'

export async function listErrors(include = 'active'): Promise<ErrorEntry[]> {
  const data = await api<{ errors: ErrorEntry[] }>(`/api/errors/?include=${encodeURIComponent(include)}`)
  return data.errors
}

export async function retryError(id: number): Promise<ErrorEntry> {
  return api<ErrorEntry>(`/api/errors/${id}/retry`, { method: 'POST' })
}

export async function dismissError(id: number): Promise<ErrorEntry> {
  return api<ErrorEntry>(`/api/errors/${id}/dismiss`, { method: 'POST' })
}

export async function listDeferred(include = 'pending'): Promise<DeferredItem[]> {
  const data = await api<{ deferred: DeferredItem[] }>(`/api/deferred/?include=${encodeURIComponent(include)}`)
  return data.deferred
}

export async function retryDeferred(id: number): Promise<DeferredItem> {
  return api<DeferredItem>(`/api/deferred/${id}/retry`, { method: 'POST' })
}

export async function discardDeferred(id: number): Promise<DeferredItem> {
  return api<DeferredItem>(`/api/deferred/${id}/discard`, { method: 'POST' })
}
