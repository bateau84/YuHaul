import type { Advisory } from '../types'
import { api } from './core'

export async function listAdvisories(): Promise<Advisory[]> {
  const data = await api<{ advisories: Advisory[] }>('/api/analytics/advisories')
  return data.advisories ?? []
}

export async function listChannelAdvisories(channelId: string): Promise<Advisory[]> {
  const data = await api<{ advisories: Advisory[] }>(
    `/api/analytics/channels/${encodeURIComponent(channelId)}/advisories`
  )
  return data.advisories ?? []
}

export async function dismissAdvisory(id: number): Promise<void> {
  await api<void>(`/api/analytics/advisories/${id}/dismiss`, { method: 'POST' })
}

export async function ignoreAdvisory(id: number): Promise<void> {
  await api<void>(`/api/analytics/advisories/${id}/ignore`, { method: 'POST' })
}

export async function actOnAdvisory(id: number, action: string): Promise<void> {
  await api<void>(`/api/analytics/advisories/${id}/act`, {
    method: 'POST',
    body: JSON.stringify({ action }),
  })
}
