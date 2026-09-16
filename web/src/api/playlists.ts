import { api } from './core'
import type { Playlist } from '../types'

export async function listPlaylists(): Promise<Playlist[]> {
  const data = await api<{ playlists: Playlist[] }>('/api/playlists')
  return data.playlists
}
