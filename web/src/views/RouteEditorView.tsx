import { useEffect, useState } from 'react'
import { useParams, useNavigate } from 'react-router-dom'
import * as api from '../api/client'
import { RouteEditor } from '../components/RouteEditor'
import { useToast } from '../components/ToastProvider'
import type { Route } from '../types'

export function RouteEditorView() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const { addToast } = useToast()
  const [route, setRoute] = useState<Route | undefined>()
  const [loading, setLoading] = useState(id !== 'new')

  useEffect(() => {
    if (id === 'new') return
    api.getRoute(Number(id)).then((r) => {
      setRoute(r)
      setLoading(false)
    })
  }, [id])

  async function handleSave(form: Route) {
    if (id === 'new') {
      await api.createRoute(form)
      addToast('Route created', 'success')
    } else {
      await api.updateRoute(Number(id), form)
      addToast('Route updated', 'success')
    }
    navigate('/routes')
  }

  async function handleDelete() {
    if (id === 'new') return
    await api.deleteRoute(Number(id))
    addToast('Route deleted', 'success')
    navigate('/routes')
  }

  if (loading) return <div className="empty-state">Loading route...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">{id === 'new' ? 'New Route' : `Edit Route: ${route?.name ?? ''}`}</h1>
      </div>
      <RouteEditor route={route} onSave={handleSave} onCancel={() => navigate('/routes')} onDelete={id === 'new' ? undefined : handleDelete} />
    </>
  )
}
