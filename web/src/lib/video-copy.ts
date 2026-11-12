import { currentLocalUnavailableLabel } from './current-local-metadata'
import { deferredReasonLabels } from './outcome'
import type { DeferredReason, MetadataAvailability, SearchAvailability, VideoSummary } from '../types'

export function formatWhen(iso: string | null | undefined): string | null {
  if (!iso) return null
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? null : d.toLocaleString()
}

export const mutedStyle = { color: 'var(--color-text-secondary)', fontSize: '0.875rem' } as const

// Echoed user text is cut to 60 characters (design: input messages).
export function echo(text: string): string {
  const chars = Array.from(text)
  return chars.length > 60 ? `${chars.slice(0, 60).join('')}…` : text
}

function toMetadata(a: SearchAvailability): MetadataAvailability {
  if (a === 'expired') return 'known_expired'
  return 'unknown'
}

// Only expiry is a known cause; anything else is "unavailable" (design).
export function titleUnavailableLabel(a: SearchAvailability): string {
  return currentLocalUnavailableLabel('title', toMetadata(a))
}

export function channelLabel(channel: VideoSummary['channel']): string {
  if (channel.name) return `Source channel: ${channel.name}`
  if (channel.channel_id) return `Channel ID: ${channel.channel_id}`
  return 'Source channel name unavailable'
}

export function reasonLabel(reason: string): string {
  return deferredReasonLabels[reason as DeferredReason] ?? reason
}

export function runLabel(id: number): string {
  return `Run ${id}`
}

