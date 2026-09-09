import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import * as api from '../api/client'
import type { Run } from '../types'

const runTypeLabels: Record<string, string> = {
  scheduled: 'Scheduled',
  manual: 'Manual',
  dry_run: 'Dry Run',
  replay: 'Replay',
  backtest: 'Backtest',
}

export function RunsView() {
  const [runs, setRuns] = useState<Run[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.listRuns().then((r) => {
      setRuns(r)
      setLoading(false)
    })
  }, [])

  if (loading) return <div className="empty-state">Loading runs...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Runs</h1>
      </div>
      {runs.length === 0 ? (
        <div className="card empty-state">No runs yet. Start a processing run to see results here.</div>
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Started</th>
                <th scope="col">Type</th>
                <th scope="col">Status</th>
                <th scope="col">Videos</th>
                <th scope="col">Quota</th>
              </tr>
            </thead>
            <tbody>
              {runs.map((r) => (
                <tr key={r.id}>
                  <td data-label="Started">
                    <Link to={`/runs/${r.id}`}>{new Date(r.started_at).toLocaleString()}</Link>
                  </td>
                  <td data-label="Type">{runTypeLabels[r.run_type] ?? r.run_type}</td>
                  <td data-label="Status">{r.status}</td>
                  <td data-label="Videos">{r.videos_discovered}</td>
                  <td data-label="Quota">{r.quota_cost}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
