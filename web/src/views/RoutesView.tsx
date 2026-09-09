import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Plus, Trash2, Edit2 } from 'lucide-react'
import * as api from '../api/client'
import { useToast } from '../components/ToastProvider'
import { RouteReorderHandle } from '../components/RouteEditor'
import type { Route } from '../types'

export function RoutesView() {
  const [routes, setRoutes] = useState<Route[]>([])
  const [loading, setLoading] = useState(true)
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
    const next = [...routes]
    const [moved] = next.splice(from, 1)
    next.splice(to, 0, moved)
    next.forEach((r, i) => (r.evaluation_order = i + 1))
    setRoutes(next)
    await api.reorderRoutes(next.map((r) => r.id))
    addToast('Route order updated', 'success')
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
                  <td data-label="Conditions">{r.conditions.length}</td>
                  <td data-label="Enabled">{r.is_enabled ? 'Yes' : 'No'}</td>
                  <td data-label="Final">{r.is_final ? 'Yes' : 'No'}</td>
                  <td data-label="Actions">
                    <div style={{ display: 'flex', gap: 8 }}>
                      <button className="icon-button" onClick={() => navigate(`/routes/${r.id}`)} aria-label={`Edit route ${r.name}`}>
                        <Edit2 size={16} />
                      </button>
                      <button
                        className="icon-button"
                        onClick={async () => {
                          await api.deleteRoute(r.id)
                          setRoutes((prev) => prev.filter((x) => x.id !== r.id))
                          addToast('Route deleted', 'success')
                        }}
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
    </>
  )
}
