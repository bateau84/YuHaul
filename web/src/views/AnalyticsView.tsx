import { useEffect, useId, useMemo, useState } from 'react'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  Legend,
  ResponsiveContainer,
  BarChart,
  Bar,
} from 'recharts'
import * as api from '../api/client'
import { routeLabel } from '../lib/route'
import type { TrendBucket, RouteMatchRate } from '../types'

// Chart row: acceptance_rate as a percentage, or null for "no data".
interface CombinedBucket extends Omit<TrendBucket, 'acceptance_rate'> {
  acceptance_rate: number | null
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString()
}

const PRESETS = ['7d', '30d', '90d', 'all']

interface RangeParams {
  from?: string
  to?: string
  error?: string
}

// Converts the selected range into the RFC3339 from/to parameters the
// analytics endpoints filter on. The backend treats `to` as an exclusive
// upper bound, so a custom to-date is extended to the next day's midnight
// to include the whole selected day.
function computeRangeParams(range: string, customFrom: string, customTo: string): RangeParams {
  if (range === 'custom') {
    if (!customFrom || !customTo) return {}
    if (customFrom > customTo) {
      return { error: 'From date must be on or before to date.' }
    }
    const to = new Date(`${customTo}T00:00:00Z`)
    to.setUTCDate(to.getUTCDate() + 1)
    return { from: `${customFrom}T00:00:00Z`, to: to.toISOString() }
  }
  const now = new Date()
  if (range === 'all') return { from: '1970-01-01T00:00:00Z' }
  const days = Number.parseInt(range, 10)
  return {
    from: new Date(now.getTime() - days * 24 * 60 * 60 * 1000).toISOString(),
    to: now.toISOString(),
  }
}

const RATE_NOTE =
  'Acceptance rate is accepted ÷ discovered, counting only runs that finished discovery. Videos accepted by other runs are not part of this rate.'
const OUTCOME_NOTE =
  'Accepted, rejected, deferred, failed and the other outcomes count every production run that has ended. Discovered counts only runs that finished discovery, so outcomes can add up to more than Discovered. Dry runs, replays and backtests are never counted.'

export function rateTooltipFormatter(value: unknown): [string, string] {
  return [
    value === null || value === undefined ? 'No data (no run finished discovery)' : `${value}% (runs that finished discovery)`,
    'Acceptance rate',
  ]
}

function sum(data: CombinedBucket[], pick: (b: CombinedBucket) => number): number {
  return data.reduce((total, b) => total + pick(b), 0)
}

// Overall rate from the summed counts (BR-023 AC13), never from rounded
// per-bucket percentages. Null when no run finished discovery.
function overallRate(data: CombinedBucket[]): { accepted: number; discovered: number; rate: string } | null {
  const discovered = sum(data, (b) => b.videos_discovered)
  if (discovered === 0) return null
  const accepted = sum(data, (b) => b.videos_accepted_discovery_finished)
  return { accepted, discovered, rate: ((accepted / discovered) * 100).toFixed(1) }
}

function summarizeAcceptance(data: CombinedBucket[]): string {
  if (data.length === 0) return 'No trend data available.'
  const overall = overallRate(data)
  if (!overall) return `${data.length} periods. No acceptance rate: no run finished discovery in this range.`
  return `${data.length} periods. Overall acceptance rate ${overall.rate}%: ${overall.accepted} accepted of ${overall.discovered} discovered, counting only runs that finished discovery.`
}

function summarizeOutcomes(data: CombinedBucket[]): string {
  if (data.length === 0) return 'No trend data available.'
  return `${data.length} periods. Across all ended production runs: ${sum(data, (b) => b.videos_accepted)} accepted, ${sum(data, (b) => b.videos_rejected)} rejected, ${sum(data, (b) => b.videos_deferred)} deferred, ${sum(data, (b) => b.videos_failed)} failed. ${sum(data, (b) => b.videos_discovered)} discovered in runs that finished discovery.`
}

// Aggregates per-bucket rates by route UUID, not display name: two routes may
// share a name and MUST stay separate series (ARS-004). The returned key is
// the chart column/dataKey, which doubles as the disambiguated legend label.
function summarizeRouteMatchRates(rates: RouteMatchRate[]): string {
  if (rates.length === 0) return 'No route match rate data available.'
  const byRoute = new Map<string, { label: string; evaluated: number; matched: number }>()
  for (const r of rates) {
    const label = routeLabel(r.route_name, r.route_id)
    const current = byRoute.get(r.route_id) ?? { label, evaluated: 0, matched: 0 }
    current.evaluated += r.evaluated
    current.matched += r.matched
    byRoute.set(r.route_id, current)
  }
  return Array.from(byRoute.values())
    .map(({ label, evaluated, matched }) => {
      const rate = evaluated > 0 ? (matched / evaluated) * 100 : 0
      return `${label}: ${rate.toFixed(1)}% match rate`
    })
    .join('. ')
}

export function AnalyticsView() {
  const [range, setRange] = useState('30d')
  const [customFrom, setCustomFrom] = useState('')
  const [customTo, setCustomTo] = useState('')
  const [data, setData] = useState<TrendBucket[]>([])
  const [routeRates, setRouteRates] = useState<RouteMatchRate[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [retryKey, setRetryKey] = useState(0)
  const [showTable, setShowTable] = useState(false)

  const trendsId = useId()
  const rateNoteId = useId()
  const outcomeNoteId = useId()
  const tableNoteId = useId()
  const routesId = useId()
  const volumesId = useId()

  const rangeParams = useMemo(
    () => computeRangeParams(range, customFrom, customTo),
    [range, customFrom, customTo]
  )
  // An incomplete custom range (only one date set) must not trigger a
  // request: the backend would silently apply its default 30d window.
  const incompleteCustom = range === 'custom' && !rangeParams.from && !rangeParams.error

  useEffect(() => {
    // A half-entered or invalid custom range keeps the previously loaded
    // charts on screen instead of issuing a misleading request.
    if (rangeParams.error || incompleteCustom) return
    let cancelled = false
    Promise.all([api.getTrends(rangeParams.from, rangeParams.to), api.getRouteMatchRates(rangeParams.from, rangeParams.to)])
      .then(([trends, rates]) => {
        if (cancelled) return
        setData(trends.buckets)
        setRouteRates(rates.routes)
        setLoading(false)
      })
      .catch((err) => {
        if (cancelled) return
        setError(err instanceof Error ? err.message : 'Failed to load analytics')
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [rangeParams.from, rangeParams.to, rangeParams.error, incompleteCustom, retryKey])

  function retry() {
    setLoading(true)
    setError(null)
    setRetryKey((k) => k + 1)
  }

  const combined = useMemo<CombinedBucket[]>(() => {
    return data.map((b) => ({
      ...b,
      acceptance_rate: b.acceptance_rate === null ? null : Number((b.acceptance_rate * 100).toFixed(1)),
    }))
  }, [data])

  // One entry per route UUID, keeping same-named routes as distinct series.
  const routeSeries = useMemo(() => {
    const byId = new Map<string, string>()
    for (const r of routeRates) {
      if (!byId.has(r.route_id)) byId.set(r.route_id, routeLabel(r.route_name, r.route_id))
    }
    return Array.from(byId, ([route_id, label]) => ({ route_id, label }))
  }, [routeRates])

  const routeRatePivot = useMemo(() => {
    const buckets = Array.from(new Set(routeRates.map((r) => r.bucket))).sort()
    return buckets.map((bucket) => {
      const row: Record<string, number | string> = { bucket }
      for (const { route_id, label } of routeSeries) {
        const rate = routeRates.find((r) => r.bucket === bucket && r.route_id === route_id)
        row[label] = rate ? Number((rate.match_rate * 100).toFixed(1)) : 0
      }
      return row
    })
  }, [routeRates, routeSeries])

  if (loading) return <div className="empty-state">Loading analytics...</div>
  if (error) {
    return (
      <div className="card" role="alert">
        <h2 className="section-title">Error loading analytics</h2>
        <p>{error}</p>
        <button className="button button--primary" onClick={retry}>Retry</button>
      </div>
    )
  }

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Analytics</h1>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
          {PRESETS.map((r) => (
            <button
              key={r}
              className={`button ${range === r ? 'button--primary' : ''}`}
              onClick={() => setRange(r)}
              aria-pressed={range === r}
            >
              {r}
            </button>
          ))}
          <button
            className={`button ${range === 'custom' ? 'button--primary' : ''}`}
            onClick={() => setRange('custom')}
            aria-pressed={range === 'custom'}
          >
            Custom
          </button>
          <button className="button" onClick={() => setShowTable((v) => !v)} aria-pressed={showTable}>
            {showTable ? 'Hide data table' : 'Show data table'}
          </button>
        </div>
      </div>

      {range === 'custom' && (
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Custom date range</h2>
          <div style={{ display: 'grid', gap: 12, gridTemplateColumns: '1fr 1fr' }}>
            <div>
              <label className="form-label" htmlFor="analytics-from-date">
                From date
              </label>
              <input
                id="analytics-from-date"
                type="date"
                className="form-input"
                value={customFrom}
                onChange={(e) => setCustomFrom(e.target.value)}
              />
            </div>
            <div>
              <label className="form-label" htmlFor="analytics-to-date">
                To date
              </label>
              <input
                id="analytics-to-date"
                type="date"
                className="form-input"
                value={customTo}
                onChange={(e) => setCustomTo(e.target.value)}
              />
            </div>
          </div>
          <p className="form-label" style={{ marginBottom: 0 }}>
            Format: YYYY-MM-DD
          </p>
          {rangeParams.error && (
            <p role="alert" style={{ color: 'var(--color-status-rejected)' }}>
              {rangeParams.error}
            </p>
          )}
        </div>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title" id={trendsId}>Acceptance rate</h2>
        <p id={rateNoteId}>{RATE_NOTE}</p>
        <p className="sr-only" id={`${trendsId}-summary`}>{summarizeAcceptance(combined)}</p>
        <div
          role="img"
          aria-labelledby={`${trendsId} ${trendsId}-summary`}
          aria-describedby={rateNoteId}
          style={{ width: '100%', minWidth: 0, height: 300 }}
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={combined}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" tickFormatter={formatDate} />
              <YAxis unit="%" />
              {/* filterNull={false}: Recharts drops a null value from the tooltip
                  by default, so a no-data period would show only its date. */}
              <Tooltip filterNull={false} formatter={rateTooltipFormatter} />
              <Legend />
              <Line
                type="monotone"
                dataKey="acceptance_rate"
                stroke="var(--color-status-accepted)"
                name="Acceptance rate"
                strokeWidth={2}
                dot={{ r: 4 }}
                activeDot={{ r: 6 }}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title" id={volumesId}>Volume & outcome breakdown</h2>
        <p id={outcomeNoteId}>{OUTCOME_NOTE}</p>
        <p className="sr-only" id={`${volumesId}-summary`}>{summarizeOutcomes(combined)}</p>
        <div
          role="img"
          aria-labelledby={`${volumesId} ${volumesId}-summary`}
          aria-describedby={outcomeNoteId}
          style={{ width: '100%', minWidth: 0, height: 300 }}
        >
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={combined}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" tickFormatter={formatDate} />
              <YAxis />
              <Tooltip />
              <Legend />
              <Bar dataKey="videos_accepted" name="Accepted" stackId="a" fill="var(--color-status-accepted)" />
              <Bar dataKey="videos_rejected" name="Rejected" stackId="a" fill="var(--color-status-rejected)" />
              <Bar dataKey="videos_deferred" name="Deferred" stackId="a" fill="var(--color-status-deferred)" />
              <Bar dataKey="videos_failed" name="Failed" stackId="a" fill="var(--color-status-failed)" />
              <Bar dataKey="videos_unavailable" name="Unavailable on YouTube" stackId="a" fill="var(--color-status-excluded)" />
              <Bar dataKey="videos_not_yet_evaluated" name="Not yet evaluated" stackId="a" fill="var(--color-status-not-evaluated)" />
            </BarChart>
          </ResponsiveContainer>
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title" id={routesId}>Route match rates</h2>
        <p className="sr-only" id={`${routesId}-summary`}>{summarizeRouteMatchRates(routeRates)}</p>
        <div
          role="img"
          aria-labelledby={`${routesId} ${routesId}-summary`}
          style={{ width: '100%', minWidth: 0, height: 300 }}
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={routeRatePivot}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" tickFormatter={formatDate} />
              <YAxis unit="%" />
              <Tooltip formatter={(value) => [`${value}%`, 'Match rate']} />
              <Legend />
              {routeSeries.map(({ route_id, label }, i) => {
                const colors = ['var(--color-status-accepted)', 'var(--color-status-in-progress)', 'var(--color-status-deferred)']
                return (
                  <Line
                    key={route_id}
                    type="monotone"
                    dataKey={label}
                    stroke={colors[i % colors.length]}
                    name={label}
                    strokeWidth={2}
                    dot={{ r: 4 }}
                    activeDot={{ r: 6 }}
                  />
                )
              })}
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>

      {showTable && (
        <div className="card">
          <h2 className="section-title">Data table</h2>
          <p id={tableNoteId}>{OUTCOME_NOTE}</p>
          <div style={{ overflowX: 'auto' }}>
            <table className="table" aria-describedby={tableNoteId}>
              <thead>
                <tr>
                  <th scope="col">Date</th>
                  <th scope="col">Discovered (runs that finished discovery)</th>
                  <th scope="col">Accepted</th>
                  <th scope="col">Acceptance rate (runs that finished discovery)</th>
                  <th scope="col">Rejected</th>
                  <th scope="col">Deferred</th>
                  <th scope="col">Failed</th>
                  <th scope="col">Unavailable on YouTube</th>
                  <th scope="col">Not yet evaluated</th>
                </tr>
              </thead>
              <tbody>
                {combined.map((b) => (
                  <tr key={b.bucket}>
                    <td data-label="Date">{formatDate(b.bucket)}</td>
                    <td data-label="Discovered (runs that finished discovery)">{b.acceptance_rate === null ? 'No data' : b.videos_discovered}</td>
                    <td data-label="Accepted">{b.videos_accepted}</td>
                    <td data-label="Acceptance rate (runs that finished discovery)">{b.acceptance_rate === null ? 'No data' : `${b.acceptance_rate}%`}</td>
                    <td data-label="Rejected">{b.videos_rejected}</td>
                    <td data-label="Deferred">{b.videos_deferred}</td>
                    <td data-label="Failed">{b.videos_failed}</td>
                    <td data-label="Unavailable on YouTube">{b.videos_unavailable}</td>
                    <td data-label="Not yet evaluated">{b.videos_not_yet_evaluated}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </>
  )
}
