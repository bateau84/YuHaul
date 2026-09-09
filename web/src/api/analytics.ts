import type { ChannelStats, ChannelDetail, TrendBucket, RouteMatchRate, RetentionPlaylist } from '../types'
import { api } from './core'

export async function listChannels(): Promise<ChannelStats[]> {
  const data = await api<{ channels: ChannelStats[] }>('/api/analytics/channels')
  return data.channels
}

export async function getChannel(id: string): Promise<ChannelDetail | undefined> {
  try {
    return await api<ChannelDetail>(`/api/analytics/channels/${encodeURIComponent(id)}`)
  } catch (err) {
    if (err instanceof Error && err.message.includes('not found')) {
      return undefined
    }
    throw err
  }
}

export async function getTrends(
  from?: string,
  to?: string,
  granularity = 'day'
): Promise<{ from: string; to: string; granularity: string; buckets: TrendBucket[] }> {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  if (granularity) params.set('granularity', granularity)
  return api(`/api/analytics/trends?${params.toString()}`)
}

export async function getRouteMatchRates(
  from?: string,
  to?: string,
  granularity = 'day'
): Promise<{ from: string; to: string; granularity: string; routes: RouteMatchRate[] }> {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  if (granularity) params.set('granularity', granularity)
  return api(`/api/analytics/route-match-rates?${params.toString()}`)
}

export async function getQuota(): Promise<{
  budget: {
    daily_limit: number
    consumed_today: number
    remaining: number
    reset_at?: string
  }
  breakdown: { operation_type: string; units_consumed: number; call_count: number }[]
}> {
  return api('/api/analytics/quota')
}

export async function listRetentionPlaylists(): Promise<RetentionPlaylist[]> {
  const data = await api<{ playlists: RetentionPlaylist[] }>('/api/settings/retention-playlists')
  return data.playlists
}

export async function createRetentionPlaylist(playlist: { playlist_id: string; playlist_name: string }): Promise<RetentionPlaylist> {
  return api<RetentionPlaylist>('/api/settings/retention-playlists', {
    method: 'POST',
    body: JSON.stringify(playlist),
  })
}

export async function deleteRetentionPlaylist(id: number): Promise<void> {
  await api<void>(`/api/settings/retention-playlists/${id}`, { method: 'DELETE' })
}
