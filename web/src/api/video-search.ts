import type { VideoSearchResponse, VideoView } from '../types'
import { api } from './core'

export interface VideoSearchQuery {
  /** Opaque cursor from a previous page; never persisted by the client. */
  cursor?: string
  /** 1-based page jump; mutually exclusive with cursor. */
  page?: number
}

// searchVideos sends the text exactly as typed; the server classifies it
// (ARS-001 §3). A bad cursor or page rejects with a 400 ApiError whose message
// is the closed code (invalid_cursor / invalid_page): this surface has no
// separate code member (ARS-001 §2).
export async function searchVideos(q: string, query: VideoSearchQuery = {}): Promise<VideoSearchResponse> {
  const params = new URLSearchParams({ q })
  if (query.cursor) params.set('cursor', query.cursor)
  if (query.page !== undefined) params.set('page', String(query.page))
  return api<VideoSearchResponse>(`/api/videos/search?${params.toString()}`)
}

export async function getVideo(videoId: string): Promise<VideoView> {
  return api<VideoView>(`/api/videos/${encodeURIComponent(videoId)}`)
}
