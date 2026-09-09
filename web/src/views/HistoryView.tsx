import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import * as api from '../api/client'
import type { ConfigVersion } from '../types'

function VersionDetail({ version }: { version: ConfigVersion }) {
  return (
    <div className="card">
      <h2 className="section-title">Version {version.id}</h2>
      <p style={{ color: 'var(--color-text-muted)' }}>{version.change_description ?? 'No description'}</p>
      <p>Created: {new Date(version.created_at).toLocaleString()}</p>
      <h3 className="section-title" style={{ marginTop: 16 }}>Routes</h3>
      {version.snapshot.routes.length === 0 ? (
        <p>No routes in this version.</p>
      ) : (
        <ul>
          {version.snapshot.routes.map((r) => (
            <li key={r.id}>
              <strong>{r.name}</strong> — {r.conditions.length} conditions, {r.actions.length} actions
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

export function HistoryView() {
  const { versionId } = useParams<{ versionId?: string }>()
  const [versions, setVersions] = useState<ConfigVersion[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.listConfigVersions().then((v) => {
      setVersions(v)
      setLoading(false)
    })
  }, [])

  if (versionId) {
    const version = versions.find((v) => v.id === Number(versionId))
    if (!version) return <div className="empty-state">Loading version...</div>
    return (
      <>
        <div className="page-header">
          <h1 className="page-title">Configuration Version {version.id}</h1>
          <Link to="/history" className="button">Back to history</Link>
        </div>
        <VersionDetail version={version} />
      </>
    )
  }

  if (loading) return <div className="empty-state">Loading history...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Configuration History</h1>
      </div>
      {versions.length === 0 ? (
        <div className="card empty-state">No configuration changes yet.</div>
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Created</th>
                <th scope="col">Description</th>
                <th scope="col">Routes</th>
              </tr>
            </thead>
            <tbody>
              {versions.map((v) => (
                <tr key={v.id}>
                  <td data-label="Created">
                    <Link to={`/history/${v.id}`}>{new Date(v.created_at).toLocaleString()}</Link>
                  </td>
                  <td data-label="Description">{v.change_description ?? '—'}</td>
                  <td data-label="Routes">{v.snapshot.routes.length}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
