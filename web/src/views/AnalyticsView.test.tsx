import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { AnalyticsView } from './AnalyticsView'
import * as api from '../api/client'
import type { RouteMatchRate } from '../types'

// The global fetch mock from src/test/setup.ts serves canned analytics data
// regardless of query parameters, so range filtering is asserted at the
// request level: these tests record the URLs the view actually requests.
const recordedRequests: string[] = []
const realFetch = globalThis.fetch

function requestsFor(path: string): URL[] {
  return recordedRequests
    .filter((u) => u.startsWith(path))
    .map((u) => new URL(u, 'http://localhost'))
}

beforeEach(() => {
  recordedRequests.length = 0
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    if (url.startsWith('/api/')) recordedRequests.push(url)
    return realFetch(input as RequestInfo)
  }) as typeof fetch
})

afterEach(() => {
  globalThis.fetch = realFetch
  vi.restoreAllMocks()
})

describe('AnalyticsView', () => {
  it('renders trend charts and a data table toggle', async () => {
    render(<AnalyticsView />)
    await waitFor(() => expect(screen.getByText('Acceptance rate')).toBeInTheDocument())

    expect(screen.getByText('Volume & outcome breakdown')).toBeInTheDocument()
    expect(screen.getByText('Route match rates')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Show data table/i }))
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
  })

  it('requests both endpoints with the default 30d range', async () => {
    render(<AnalyticsView />)
    await waitFor(() => expect(screen.getByText('Acceptance rate')).toBeInTheDocument())

    const trends = requestsFor('/api/analytics/trends')
    const rates = requestsFor('/api/analytics/route-match-rates')
    expect(trends).toHaveLength(1)
    expect(rates).toHaveLength(1)

    const from = trends[0].searchParams.get('from')
    const to = trends[0].searchParams.get('to')
    expect(from).toBeTruthy()
    expect(to).toBeTruthy()
    const span = new Date(to!).getTime() - new Date(from!).getTime()
    expect(span).toBeGreaterThan(29 * 24 * 3600 * 1000)
    expect(span).toBeLessThan(31 * 24 * 3600 * 1000)

    // Both endpoints must receive the identical window.
    expect(rates[0].searchParams.get('from')).toBe(from)
    expect(rates[0].searchParams.get('to')).toBe(to)
  })

  it('refetches both endpoints when a preset range is selected', async () => {
    render(<AnalyticsView />)
    await waitFor(() => expect(screen.getByText('Acceptance rate')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: '7d' }))

    await waitFor(() => expect(requestsFor('/api/analytics/trends')).toHaveLength(2))
    const trends = requestsFor('/api/analytics/trends')
    const rates = requestsFor('/api/analytics/route-match-rates')
    expect(rates).toHaveLength(2)

    const from = trends[1].searchParams.get('from')
    const to = trends[1].searchParams.get('to')
    const span = new Date(to!).getTime() - new Date(from!).getTime()
    expect(span).toBeGreaterThan(6 * 24 * 3600 * 1000)
    expect(span).toBeLessThan(8 * 24 * 3600 * 1000)
    expect(rates[1].searchParams.get('from')).toBe(from)
    expect(rates[1].searchParams.get('to')).toBe(to)
  })

  it('requests the full history when the all preset is selected', async () => {
    render(<AnalyticsView />)
    await waitFor(() => expect(screen.getByText('Acceptance rate')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: 'all' }))

    await waitFor(() => expect(requestsFor('/api/analytics/trends')).toHaveLength(2))
    const trends = requestsFor('/api/analytics/trends')
    const rates = requestsFor('/api/analytics/route-match-rates')
    expect(trends[1].searchParams.get('from')).toBe('1970-01-01T00:00:00Z')
    expect(rates[1].searchParams.get('from')).toBe('1970-01-01T00:00:00Z')
  })

  it('requests both endpoints with a custom date range', async () => {
    render(<AnalyticsView />)
    await waitFor(() => expect(screen.getByText('Acceptance rate')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: 'Custom' }))
    fireEvent.change(screen.getByLabelText('From date'), { target: { value: '2026-01-01' } })
    fireEvent.change(screen.getByLabelText('To date'), { target: { value: '2026-01-31' } })

    await waitFor(() => expect(requestsFor('/api/analytics/trends')).toHaveLength(2))
    const trends = requestsFor('/api/analytics/trends')
    const rates = requestsFor('/api/analytics/route-match-rates')
    expect(rates).toHaveLength(2)

    // The backend treats `to` as exclusive, so the selected to-date is
    // extended to the next day's midnight to include the whole day.
    expect(trends[1].searchParams.get('from')).toBe('2026-01-01T00:00:00Z')
    expect(trends[1].searchParams.get('to')).toBe('2026-02-01T00:00:00.000Z')
    expect(rates[1].searchParams.get('from')).toBe('2026-01-01T00:00:00Z')
    expect(rates[1].searchParams.get('to')).toBe('2026-02-01T00:00:00.000Z')
  })

  it('shows a validation error and stops refetching for an inverted custom range', async () => {
    render(<AnalyticsView />)
    await waitFor(() => expect(screen.getByText('Acceptance rate')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: 'Custom' }))
    fireEvent.change(screen.getByLabelText('From date'), { target: { value: '2026-02-01' } })
    fireEvent.change(screen.getByLabelText('To date'), { target: { value: '2026-01-01' } })

    await waitFor(() =>
      expect(screen.getByText('From date must be on or before to date.')).toBeInTheDocument()
    )
    // No further requests were issued after the invalid range was applied.
    expect(requestsFor('/api/analytics/trends')).toHaveLength(1)
    expect(requestsFor('/api/analytics/route-match-rates')).toHaveLength(1)
  })

  // Two routes may share a display name (BR-003 AC10); the route-match chart
  // must keep them as separate series keyed by UUID, labeled with the short
  // UUID suffix (ARS-004 verification point 8).
  it('keeps same-named routes as distinct series labeled by short UUID', async () => {
    const routeIdA = '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce'
    const routeIdB = 'b7e9d4f2-1c2a-4d3b-8e5f-9a0b1c2d3e4f'
    const rates: RouteMatchRate[] = [
      { bucket: '2026-08-26T00:00:00Z', route_id: routeIdA, route_name: 'Ambient', evaluated: 4, matched: 2, match_rate: 0.5 },
      { bucket: '2026-08-26T00:00:00Z', route_id: routeIdB, route_name: 'Ambient', evaluated: 8, matched: 1, match_rate: 0.125 },
    ]
    vi.spyOn(api, 'getRouteMatchRates').mockResolvedValue({
      from: '',
      to: '',
      granularity: 'day',
      routes: rates,
    })

    render(<AnalyticsView />)

    // The accessible series summary keeps the two same-named routes separate,
    // each identified by its short UUID suffix and its own aggregate rate.
    await waitFor(() =>
      expect(
        screen.getByText('Ambient (3a554e96): 50.0% match rate. Ambient (b7e9d4f2): 12.5% match rate')
      ).toBeInTheDocument()
    )
  })
})
