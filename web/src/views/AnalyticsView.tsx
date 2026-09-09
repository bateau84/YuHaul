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
import type { TrendBucket, RouteMatchRate } from '../types'

interface CombinedBucket extends TrendBucket {
  acceptance_rate: number
}

function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString()
}

function summarizeTrends(data: CombinedBucket[]): string {
  if (data.length === 0) return 'No trend data available.'
  const totalDiscovered = data.reduce((sum, b) => sum + b.discovered, 0)
  const totalAccepted = data.reduce((sum, b) => sum + b.accepted, 0)
  const avgRate = totalDiscovered > 0 ? (totalAccepted / totalDiscovered) * 100 : 0
  return `${data.length} periods. ${totalDiscovered} discovered, ${totalAccepted} accepted. Average acceptance rate ${avgRate.toFixed(1)}%.`
}

function summarizeRouteMatchRates(rates: RouteMatchRate[]): string {
  if (rates.length === 0) return 'No route match rate data available.'
  const byRoute = new Map<string, { evaluated: number; matched: number }>()
  for (const r of rates) {
    const current = byRoute.get(r.route_name) ?? { evaluated: 0, matched: 0 }
    current.evaluated += r.evaluated
    current.matched += r.matched
    byRoute.set(r.route_name, current)
  }
  return Array.from(byRoute.entries())
    .map(([name, { evaluated, matched }]) => {
      const rate = evaluated > 0 ? (matched / evaluated) * 100 : 0
      return `${name}: ${rate.toFixed(1)}% match rate`
    })
    .join('. ')
}

export function AnalyticsView() {
  const [range, setRange] = useState('30d')
  const [data, setData] = useState<TrendBucket[]>([])
  const [routeRates, setRouteRates] = useState<RouteMatchRate[]>([])
  const [loading, setLoading] = useState(true)
  const [showTable, setShowTable] = useState(false)

  const trendsId = useId()
  const routesId = useId()
  const volumesId = useId()

  useEffect(() => {
    setLoading(true)
    Promise.all([api.getTrends(), api.getRouteMatchRates()]).then(([trends, rates]) => {
      setData(trends.buckets)
      setRouteRates(rates.routes)
      setLoading(false)
    })
  }, [range])

  const combined = useMemo<CombinedBucket[]>(() => {
    return data.map((b) => ({
      ...b,
      acceptance_rate: b.discovered > 0 ? Number(((b.accepted / b.discovered) * 100).toFixed(1)) : 0,
    }))
  }, [data])

  const routeRatePivot = useMemo(() => {
    const buckets = Array.from(new Set(routeRates.map((r) => r.bucket))).sort()
    const routeNames = Array.from(new Set(routeRates.map((r) => r.route_name)))
    return buckets.map((bucket) => {
      const row: Record<string, number | string> = { bucket }
      for (const name of routeNames) {
        const rate = routeRates.find((r) => r.bucket === bucket && r.route_name === name)
        row[name] = rate ? Number((rate.match_rate * 100).toFixed(1)) : 0
      }
      return row
    })
  }, [routeRates])

  if (loading) return <div className="empty-state">Loading analytics...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Analytics</h1>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
          {['7d', '30d', '90d'].map((r) => (
            <button
              key={r}
              className={`button ${range === r ? 'button--primary' : ''}`}
              onClick={() => setRange(r)}
              aria-pressed={range === r}
            >
              {r}
            </button>
          ))}
          <button className="button" onClick={() => setShowTable((v) => !v)} aria-pressed={showTable}>
            {showTable ? 'Hide data table' : 'Show data table'}
          </button>
        </div>
      </div>

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
              {Array.from(new Set(routeRates.map((r) => r.route_name))).map((name, i) => {
                const colors = ['var(--color-status-accepted)', 'var(--color-status-in-progress)', 'var(--color-status-deferred)']
                return (
                  <Line
                    key={name}
                    type="monotone"
                    dataKey={name}
                    stroke={colors[i % colors.length]}
                    name={name}
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
