import { useEffect, useState } from 'react'
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
import type { TrendBucket } from '../types'

export function AnalyticsView() {
  const [range, setRange] = useState('30d')
  const [data, setData] = useState<TrendBucket[]>([])
  const [loading, setLoading] = useState(true)
  const [showTable, setShowTable] = useState(false)

  useEffect(() => {
    api.getTrends().then((res) => {
      setData(res.buckets)
      setLoading(false)
    })
  }, [range])

  if (loading) return <div className="empty-state">Loading analytics...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Analytics</h1>
        <div style={{ display: 'flex', gap: 8 }}>
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
          <button className="button" onClick={() => setShowTable((v) => !v)}>
            {showTable ? 'Hide table' : 'Show data table'}
          </button>
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Acceptance rate & volume</h2>
        <div style={{ minWidth: 300, height: 300 }}>
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={data}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" tickFormatter={(v) => new Date(v).toLocaleDateString()} />
              <YAxis />
              <Tooltip />
              <Legend />
              <Line type="monotone" dataKey="accepted" stroke="var(--color-status-accepted)" name="Accepted" />
              <Line type="monotone" dataKey="discovered" stroke="var(--color-status-in-progress)" name="Discovered" />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Outcome breakdown</h2>
        <div style={{ minWidth: 300, height: 300 }}>
          <ResponsiveContainer width="100%" height="100%">
            <BarChart data={data}>
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis dataKey="bucket" tickFormatter={(v) => new Date(v).toLocaleDateString()} />
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

      {showTable && (
        <div className="card">
          <h2 className="section-title">Data table</h2>
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Date</th>
                <th scope="col">Discovered</th>
                <th scope="col">Accepted</th>
                <th scope="col">Rejected</th>
                <th scope="col">Deferred</th>
                <th scope="col">Failed</th>
              </tr>
            </thead>
            <tbody>
              {data.map((b) => (
                <tr key={b.bucket}>
                  <td data-label="Date">{new Date(b.bucket).toLocaleDateString()}</td>
                  <td data-label="Discovered">{b.discovered}</td>
                  <td data-label="Accepted">{b.accepted}</td>
                  <td data-label="Rejected">{b.rejected}</td>
                  <td data-label="Deferred">{b.deferred}</td>
                  <td data-label="Failed">{b.failed}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
