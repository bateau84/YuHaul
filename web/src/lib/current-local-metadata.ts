import type { MetadataAvailability } from '../types'

export type CurrentLocalField = 'title' | 'channel_name' | 'duration_seconds'

const fieldNames: Record<CurrentLocalField, { unavailable: string; expired: string; withheld: string }> = {
  title: {
    unavailable: 'Video title unavailable',
    expired: 'Video title unavailable — local metadata expired',
    withheld: 'Video title unavailable for display',
  },
  channel_name: {
    unavailable: 'Source channel name unavailable',
    expired: 'Source channel name unavailable — local metadata expired',
    withheld: 'Source channel name unavailable for display',
  },
  duration_seconds: {
    unavailable: 'Duration unavailable',
    expired: 'Duration unavailable — local metadata expired',
    withheld: 'Duration unavailable for display',
  },
}

export function currentLocalUnavailableLabel(field: CurrentLocalField, availability: MetadataAvailability | undefined): string {
  if (availability === 'known_expired') return fieldNames[field].expired
  if (availability === 'withheld') return fieldNames[field].withheld
  return fieldNames[field].unavailable
}

export function currentLocalObservedAt(sourceFetchedAt?: string | null): string | undefined {
  if (!sourceFetchedAt) return undefined
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?(?:Z|([+-])(\d{2}):(\d{2}))$/.exec(sourceFetchedAt)
  if (!match) return undefined

  const [, yearText, monthText, dayText, hourText, minuteText, secondText, , , offsetHourText, offsetMinuteText] = match
  const year = Number(yearText)
  const month = Number(monthText)
  const day = Number(dayText)
  const hour = Number(hourText)
  const minute = Number(minuteText)
  const second = Number(secondText)
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
  const daysInMonth = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]

  if (
    month < 1 || month > 12 || day < 1 || day > daysInMonth[month - 1]
    || hour > 23 || minute > 59 || second > 59
    || (offsetHourText !== undefined && (Number(offsetHourText) > 23 || Number(offsetMinuteText) > 59))
  ) return undefined

  return `Observed at ${sourceFetchedAt}`
}
