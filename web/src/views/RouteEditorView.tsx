import { useEffect, useState } from 'react'
import { useParams, useNavigate, useMatch, Link } from 'react-router-dom'
import * as api from '../api/client'
import { RouteEditor } from '../components/RouteEditor'
import { useToast } from '../hooks/useToast'
import type { Route } from '../types'

function blankRoute(evaluationOrder: number): Route {
  return {
    id: 0,
    name: '',
    description: '',
    evaluation_order: evaluationOrder,
    is_final: false,
    is_enabled: true,
    condition_composition: 'AND',
    conditions: [],
    actions: [],
    exclusions: [],
    created_at: '',
    updated_at: '',
  }
}

export function RouteEditorView() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { addToast } = useToast()
  const isNew = useMatch('/routes/new') !== null
  const [route, setRoute] = useState<Route | undefined>()
  const [existingRoutes, setExistingRoutes] = useState<Route[]>([])
  const [nextOrder, setNextOrder] = useState(1)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<'not-found' | 'network' | null>(null)
  const [retryCount, setRetryCount] = useState(0)

  useEffect(() => {
    let cancelled = false
    async function load() {
      try {
        const routes = await api.listRoutes()
        if (cancelled) return
        setExistingRoutes(routes)
        const maxOrder = routes.length > 0 ? Math.max(...routes.map((r) => r.evaluation_order)) : 0
        setNextOrder(maxOrder + 1)

        if (!isNew) {
          const r = await api.getRoute(Number(id))
          if (cancelled) return
          if (r === undefined) {
            setError('not-found')
          } else {
            setRoute(r)
          }
        }
      } catch {
        if (!cancelled) setError('network')
      } finally {
        if (!cancelled) setLoading(false)
      }
    }
    load()
    return () => {
      cancelled = true
    }
  }, [id, isNew, retryCount])

  const initialRoute = isNew ? blankRoute(nextOrder) : route

  async function handleSave(form: Route) {
    try {
      if (isNew) {
        await api.createRoute(form)
        addToast('Route created', 'success')
      } else {
        await api.updateRoute(Number(id), form)
        addToast('Route updated', 'success')
      }
      navigate('/routes')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to save route', 'error')
    }
  }

  async function handleDelete() {
    if (isNew) return
    try {
      await api.deleteRoute(Number(id))
      addToast('Route deleted', 'success')
      navigate('/routes')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to delete route', 'error')
    }
  }

  if (loading) return <div className="empty-state">Loading route...</div>

  if (error === 'not-found') {
    return (
      <div className="card empty-state">
        <h1 className="page-title">Route not found</h1>
        <p>The route you are looking for does not exist.</p>
        <Link to="/routes" className="button button--primary" style={{ marginTop: 16 }}>
          Back to Routes
        </Link>
      </div>
    )
  }

  if (error === 'network') {
    return (
      <div className="card empty-state">
        <h1 className="page-title">Failed to load route</h1>
        <p>Could not load the route. Please check your connection and try again.</p>
        <button
          type="button"
          className="button button--primary"
          style={{ marginTop: 16 }}
          onClick={() => {
            setError(null)
            setRetryCount((c) => c + 1)
          }}
        >
          Retry
        </button>
      </div>
    )
  }

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">{isNew ? 'New Route' : `Edit Route: ${route?.name ?? ''}`}</h1>
      </div>
      <RouteEditor
        key={isNew ? 'new' : route?.id ?? id}
        route={initialRoute}
        existingRoutes={existingRoutes}
        onSave={handleSave}
        onCancel={() => navigate('/routes')}
        onDelete={isNew ? undefined : handleDelete}
      />
    </>
  )
}
