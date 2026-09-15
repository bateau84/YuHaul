import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import * as api from '../api/client'
import { useToast } from '../hooks/useToast'
import type { ConfigVersion, ConfigDiffResult, Route } from '../types'

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString()
}

function RouteSnapshot({ route }: { route: Route }) {
  return (
    <div style={{ marginBottom: 12, padding: 12, border: '1px solid var(--color-border-default)', borderRadius: 8 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <strong>{route.name}</strong>
        <div style={{ display: 'flex', gap: 8 }}>
          {route.is_final && <span className="badge badge--info">Final</span>}
          {!route.is_enabled && <span className="badge badge--muted">Disabled</span>}
        </div>
      </div>
      <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', margin: '4px 0' }}>{route.description}</p>
      <p style={{ fontSize: '0.875rem' }}>Order: {route.evaluation_order} · Composition: {route.condition_composition}</p>
      {route.conditions.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <p style={{ fontSize: '0.875rem', fontWeight: 600 }}>Conditions</p>
          <ul style={{ fontSize: '0.875rem', margin: '4px 0' }}>
            {route.conditions.map((c) => (
              <li key={c.id ?? c.condition_index}>{c.field} {c.operator} "{c.value}"</li>
            ))}
          </ul>
        </div>
      )}
      {route.actions.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <p style={{ fontSize: '0.875rem', fontWeight: 600 }}>Actions</p>
          <ul style={{ fontSize: '0.875rem', margin: '4px 0' }}>
            {route.actions.map((a) => (
              <li key={a.id ?? a.action_index}>{a.action_type} → {a.target_playlist_id}</li>
            ))}
          </ul>
        </div>
      )}
      {route.exclusions.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <p style={{ fontSize: '0.875rem', fontWeight: 600 }}>Exclusions</p>
          <ul style={{ fontSize: '0.875rem', margin: '4px 0' }}>
            {route.exclusions.map((e) => (
              <li key={e.id ?? `${e.exclusion_type}-${e.exclusion_value}`}>{e.exclusion_type}: {e.exclusion_value}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

function DiffPanel({ diff }: { diff: ConfigDiffResult }) {
  const hasChanges =
    Object.keys(diff.settings_changed).length > 0 ||
    diff.routes_added.length > 0 ||
    diff.routes_removed.length > 0 ||
    diff.routes_changed.length > 0

  return (
    <div>
      <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
        Comparing version {diff.from_id} → version {diff.to_id}
      </p>
      {!hasChanges && <p>No changes between these versions.</p>}
      {Object.keys(diff.settings_changed).length > 0 && (
        <div style={{ marginTop: 12 }}>
          <h4 className="section-title">Settings changed</h4>
          <ul>
            {Object.entries(diff.settings_changed).map(([key, change]) => (
              <li key={key}>
                {key}: <span style={{ color: 'var(--color-danger)' }}>{change.old}</span> →{' '}
                <span style={{ color: 'var(--color-success)' }}>{change.new}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      {diff.routes_added.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <h4 className="section-title">Routes added</h4>
          {diff.routes_added.map((r) => (
            <div key={r.id} style={{ padding: 8, background: 'rgba(34, 197, 94, 0.1)', borderRadius: 4 }}>
              <strong>{r.name}</strong>
            </div>
          ))}
        </div>
      )}
      {diff.routes_removed.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <h4 className="section-title">Routes removed</h4>
          {diff.routes_removed.map((r) => (
            <div key={r.id} style={{ padding: 8, background: 'rgba(239, 68, 68, 0.1)', borderRadius: 4 }}>
              <strong>{r.name}</strong>
            </div>
          ))}
        </div>
      )}
      {diff.routes_changed.length > 0 && (
        <div style={{ marginTop: 12 }}>
          <h4 className="section-title">Routes changed</h4>
          {diff.routes_changed.map((rc) => (
            <div key={rc.id} style={{ padding: 8, background: 'rgba(245, 158, 11, 0.1)', borderRadius: 4 }}>
              <strong>{rc.old.name}</strong>
              <ul style={{ marginTop: 4 }}>
                {rc.changes.map((c, idx) => (
                  <li key={idx}>
                    {c.field}: <span style={{ color: 'var(--color-danger)' }}>{c.old}</span> →{' '}
                    <span style={{ color: 'var(--color-success)' }}>{c.new}</span>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function VersionDetail({ version }: { version: ConfigVersion }) {
  const [diff, setDiff] = useState<ConfigDiffResult | null>(null)
  const [diffLoading, setDiffLoading] = useState(true)
  const { addToast } = useToast()

  useEffect(() => {
    api
      .diffConfigVersion(version.id)
      .then((d) => setDiff(d))
      .catch((err) => addToast(err instanceof Error ? err.message : 'Failed to load diff', 'error'))
      .finally(() => setDiffLoading(false))
  }, [version.id, addToast])

  async function restore() {
    try {
      await api.restoreConfigVersion(version.id, `Restored from version ${version.id}`)
      addToast(`Configuration restored from version ${version.id}`, 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to restore configuration', 'error')
    }
  }

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Configuration Version {version.id}</h1>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="button button--primary" onClick={restore}>
            Restore this version
          </button>
          <Link to="/history" className="button">
            Back to history
          </Link>
        </div>
      </div>
      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Summary</h2>
        <p style={{ color: 'var(--color-text-muted)' }}>{version.change_description ?? 'No description'}</p>
        <p>Created: {formatDate(version.created_at)}</p>
        <p>Routes: {version.snapshot.routes.length}</p>
        <p>Duplicate threshold: {version.snapshot.duplicate_threshold}</p>
      </div>
      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Diff vs previous version</h2>
        {diffLoading && <p>Loading diff...</p>}
        {!diffLoading && diff && <DiffPanel diff={diff} />}
      </div>
      <div className="card">
        <h2 className="section-title">Route snapshot</h2>
        {version.snapshot.routes.length === 0 ? (
          <p>No routes in this version.</p>
        ) : (
          version.snapshot.routes.map((r) => <RouteSnapshot key={r.id} route={r} />)
        )}
      </div>
    </>
  )
}

export function HistoryView() {
  const { versionId } = useParams<{ versionId?: string }>()
  const [versions, setVersions] = useState<ConfigVersion[]>([])
  const [loading, setLoading] = useState(true)
  const { addToast } = useToast()

  useEffect(() => {
    let cancelled = false
    api
      .listConfigVersions()
      .then((v) => {
        if (cancelled) return
        setVersions(v)
        setLoading(false)
      })
      .catch((err) => {
        if (cancelled) return
        addToast(err instanceof Error ? err.message : 'Failed to load history', 'error')
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [addToast])

  async function restore(id: number) {
    try {
      await api.restoreConfigVersion(id, `Restored from version ${id}`)
      const updated = await api.listConfigVersions()
      setVersions(updated)
      addToast(`Configuration restored from version ${id}`, 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to restore configuration', 'error')
    }
  }

  if (versionId) {
    const version = versions.find((v) => v.id === Number(versionId))
    if (!version) return <div className="empty-state">Loading version...</div>
    return <VersionDetail version={version} />
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
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {versions.map((v) => (
                <tr key={v.id}>
                  <td data-label="Created">
                    <Link to={`/history/${v.id}`}>{formatDate(v.created_at)}</Link>
                  </td>
                  <td data-label="Description">{v.change_description ?? '—'}</td>
                  <td data-label="Routes">{v.snapshot.routes.length}</td>
                  <td data-label="Actions">
                    <div style={{ display: 'flex', gap: 8 }}>
                      <Link to={`/history/${v.id}`} className="button">
                        View
                      </Link>
                      <button className="button" onClick={() => restore(v.id)}>
                        Restore
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
