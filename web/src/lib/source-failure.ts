import type { SourceFailure } from '../types'

// Sentences for a recorded uploads-source failure (design: "Channel scan
// history and coverage block"). Chosen from the server-admitted reason only;
// never a status number, playlist ID, URL or raw text.

export const UPLOADS_NOT_SCANNED = 'Uploads could not be scanned.'

export function sourceReasonSentence(f: Pick<SourceFailure, 'reason'>): string {
  return f.reason === 'playlistNotFound'
    ? 'YouTube reported that the uploads playlist was not found.'
    : 'The specific reason is unavailable.'
}

// The channel line: its name, else its ID, else "Channel details unavailable."
export function sourceChannelLabel(f: Pick<SourceFailure, 'channel_name' | 'channel_id'>): string {
  return f.channel_name || f.channel_id || 'Channel details unavailable.'
}

// The ID follows on its own line when it is valid and is not already the
// first line.
export function showSourceChannelId(f: Pick<SourceFailure, 'channel_name' | 'channel_id'>): boolean {
  return Boolean(f.channel_id) && Boolean(f.channel_name)
}
