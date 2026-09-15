import { useEffect, useState } from 'react'
import { useParams, useNavigate, useMatch } from 'react-router-dom'
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
  const [nextOrder, setNextOrder] = useState(1)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    if (isNew) {
      api.listRoutes().then((routes) => {
        const maxOrder = routes.length > 0 ? Math.max(...routes.map((r) => r.evaluation_order)) : 0
        setNextOrder(maxOrder + 1)
        setLoading(false)
      })
      return
    }
    api.getRoute(Number(id)).then((r) => {
      setRoute(r)
      setLoading(false)
    })
  }, [id, isNew])

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

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">{isNew ? 'New Route' : `Edit Route: ${route?.name ?? ''}`}</h1>
      </div>
      <RouteEditor
        key={isNew ? 'new' : route?.id ?? id}
        route={initialRoute}
        onSave={handleSave}
        onCancel={() => navigate('/routes')}
        onDelete={isNew ? undefined : handleDelete}
      />
    </>
  )
}
