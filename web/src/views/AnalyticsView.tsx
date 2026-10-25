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

interface CombinedBucket extends TrendBucket {
  acceptance_rate: number
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

function summarizeTrends(data: CombinedBucket[]): string {
  if (data.length === 0) return 'No trend data available.'
  const totalDiscovered = data.reduce((sum, b) => sum + b.discovered, 0)
  const totalAccepted = data.reduce((sum, b) => sum + b.accepted, 0)
  const avgRate = totalDiscovered > 0 ? (totalAccepted / totalDiscovered) * 100 : 0
  return `${data.length} periods. ${totalDiscovered} discovered, ${totalAccepted} accepted. Average acceptance rate ${avgRate.toFixed(1)}%.`
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
      acceptance_rate: b.discovered > 0 ? Number(((b.accepted / b.discovered) * 100).toFixed(1)) : 0,
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
        <p className="sr-only" id={`${trendsId}-summary`}>{summarizeTrends(combined)}</p>
        <div
          role="img"
          aria-labelledby={`${trendsId} ${trendsId}-summary`}
          style={{ minWidth: 300, height: 300 }}
        >
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={combined}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" tickFormatter={formatDate} />
              <YAxis unit="%" />
              <Tooltip formatter={(value) => [`${value}%`, 'Acceptance rate']} />
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
        <p className="sr-only" id={`${volumesId}-summary`}>
          {summarizeTrends(combined).replace('Average acceptance rate', 'Overall acceptance rate')}
        </p>
        <div
          role="img"
          aria-labelledby={`${volumesId} ${volumesId}-summary`}
          style={{ minWidth: 300, height: 300 }}
        >
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={combined}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" tickFormatter={formatDate} />
              <YAxis />
              <Tooltip />
              <Legend />
              <Bar dataKey="accepted" stackId="a" fill="var(--color-status-accepted)" />
              <Bar dataKey="rejected" stackId="a" fill="var(--color-status-rejected)" />
              <Bar dataKey="deferred" stackId="a" fill="var(--color-status-deferred)" />
              <Bar dataKey="failed" stackId="a" fill="var(--color-status-failed)" />
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
          style={{ minWidth: 300, height: 300 }}
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
          <div style={{ overflowX: 'auto' }}>
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Date</th>
                  <th scope="col">Discovered</th>
                  <th scope="col">Accepted</th>
                  <th scope="col">Acceptance rate</th>
                  <th scope="col">Rejected</th>
                  <th scope="col">Deferred</th>
                  <th scope="col">Failed</th>
                </tr>
              </thead>
              <tbody>
                {combined.map((b) => (
                  <tr key={b.bucket}>
                    <td data-label="Date">{formatDate(b.bucket)}</td>
                    <td data-label="Discovered">{b.discovered}</td>
                    <td data-label="Accepted">{b.accepted}</td>
                    <td data-label="Acceptance rate">{b.acceptance_rate}%</td>
                    <td data-label="Rejected">{b.rejected}</td>
                    <td data-label="Deferred">{b.deferred}</td>
                    <td data-label="Failed">{b.failed}</td>
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
