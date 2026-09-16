import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Plus, Trash2, Edit2, Check, X } from 'lucide-react'
import * as api from '../api/client'
import { useToast } from '../hooks/useToast'
import { RouteReorderHandle } from '../components/RouteEditor'
import { DeleteConfirmDialog } from '../components/DeleteConfirmDialog'
import type { Route, RouteCondition } from '../types'

const fieldLabels: Record<RouteCondition['field'], string> = {
  title: 'title',
  channel_id: 'channel ID',
  channel_name: 'channel name',
  description: 'description',
  duration_seconds: 'duration',
}

const operatorLabels: Record<RouteCondition['operator'], string> = {
  contains: 'contains',
  equals: 'equals',
  starts_with: 'starts with',
  ends_with: 'ends with',
  matches_regex: 'matches regex',
  greater_than: '>',
  less_than: '<',
  between: 'between',
}

function formatConditionValue(c: RouteCondition): string {
  if (c.operator === 'between') {
    const parts = c.value.split(',')
    const min = parts[0]?.trim() ?? ''
    const max = parts[1]?.trim() ?? ''
    return `${min}–${max}`
  }
  return c.value
}

function conditionSummary(route: Route): string {
  const conditions = route.conditions ?? []
  if (conditions.length === 0) return 'No conditions'
  const parts = conditions.map((c) => {
    const field = fieldLabels[c.field]
    const op = operatorLabels[c.operator]
    const value = formatConditionValue(c)
    return `${field} ${op} ${value}`
  })
  const summary = parts.join(` ${route.condition_composition} `)
  if (summary.length <= 60) return summary
  return `${summary.slice(0, 57)}…`
}

export function RoutesView() {
  const [routes, setRoutes] = useState<Route[]>([])
  const [loading, setLoading] = useState(true)
  const [deletingRoute, setDeletingRoute] = useState<Route | null>(null)
  const { addToast } = useToast()
  const navigate = useNavigate()

  useEffect(() => {
    api.listRoutes().then((r) => {
      setRoutes(r)
      setLoading(false)
    })
  }, [])

  async function move(from: number, to: number) {
    if (to < 0 || to >= routes.length) return
    const previous = routes
    const next = routes.map((r) => ({ ...r }))
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    next.forEach((r, i) => (r.evaluation_order = i + 1))
    setRoutes(next)
    try {
      await api.reorderRoutes(next.map((r) => r.id))
      addToast('Route order updated', 'success')
    } catch (err) {
      setRoutes(previous)
      addToast(err instanceof Error ? err.message : 'Failed to update route order', 'error')
    }
  }

  async function toggleEnabled(route: Route) {
    const updated = { ...route, is_enabled: !route.is_enabled }
    const previous = routes
    setRoutes((prev) => prev.map((r) => (r.id === route.id ? updated : r)))
    try {
      const { id: _id, created_at: _created, updated_at: _updated, ...payload } = updated
      await api.updateRoute(route.id, payload)
      addToast(updated.is_enabled ? 'Route enabled' : 'Route disabled', 'success')
    } catch (err) {
      setRoutes(previous)
      addToast(err instanceof Error ? err.message : 'Failed to update route', 'error')
    }
  }

  async function confirmDelete(route: Route) {
    setDeletingRoute(null)
    try {
      await api.deleteRoute(route.id)
      setRoutes((prev) => prev.filter((r) => r.id !== route.id))
      addToast('Route deleted', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to delete route', 'error')
    }
  }

  if (loading) return <div className="empty-state">Loading routes...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Routes</h1>
        <Link to="/routes/new" className="button button--primary">
          <Plus size={16} aria-hidden="true" /> New Route
        </Link>
      </div>
      {routes.length === 0 ? (
        <div className="card empty-state">
          <p>No routes yet. Create your first route to start routing videos.</p>
          <Link to="/routes/new" className="button button--primary" style={{ marginTop: 16 }}>
            New Route
          </Link>
        </div>
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Order</th>
                <th scope="col">Name</th>
                <th scope="col">Conditions</th>
                <th scope="col">Enabled</th>
                <th scope="col">Final</th>
                <th scope="col">Matches</th>
                <th scope="col">Actions</th>
              </tr>
            </thead>
            <tbody>
              {routes.map((r, i) => (
                <tr key={r.id}>
                  <td data-label="Order">
                    <RouteReorderHandle name={r.name} index={i} total={routes.length} onMove={move} />
                  </td>
                  <td data-label="Name">
                    <Link to={`/routes/${r.id}`}>{r.name}</Link>
                    <p style={{ margin: 0, color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>{r.description}</p>
                  </td>
                  <td data-label="Conditions">
                    <span style={{ color: (r.conditions ?? []).length === 0 ? 'var(--color-text-muted)' : undefined }}>
                      {conditionSummary(r)}
                    </span>
                  </td>
                  <td data-label="Enabled">
                    <label
                      style={{ display: 'inline-flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}
                      aria-label={r.is_enabled ? `Disable route ${r.name}` : `Enable route ${r.name}`}
                    >
                      <span className="switch">
                        <span className="switch-track" aria-hidden="true" />
                        <input
                          type="checkbox"
                          role="switch"
                          aria-checked={r.is_enabled}
                          checked={r.is_enabled}
                          onChange={() => toggleEnabled(r)}
                        />
                        <span className="switch-thumb" aria-hidden="true">
                          {r.is_enabled ? <Check size={12} /> : <X size={12} />}
                        </span>
                      </span>
                      <span style={{ color: r.is_enabled ? 'var(--color-status-accepted)' : 'var(--color-status-rejected)', fontWeight: 500 }}>
                        {r.is_enabled ? 'Enabled' : 'Disabled'}
                      </span>
                    </label>
                  </td>
                  <td data-label="Final">{r.is_final && <span className="badge badge--accepted">Final</span>}</td>
                  <td data-label="Matches">{r.match_count ?? '—'}</td>
                  <td data-label="Actions">
                    <div style={{ display: 'flex', gap: 8 }}>
                      <button className="icon-button" onClick={() => navigate(`/routes/${r.id}`)} aria-label={`Edit route ${r.name}`}>
                        <Edit2 size={16} />
                      </button>
                      <button
                        className="icon-button"
                        onClick={() => setDeletingRoute(r)}
                        aria-label={`Delete route ${r.name}`}
                      >
                        <Trash2 size={16} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {deletingRoute && (
        <DeleteConfirmDialog
          routeName={deletingRoute.name}
          onCancel={() => setDeletingRoute(null)}
          onConfirm={() => confirmDelete(deletingRoute)}
        />
      )}
    </>
  )
}
