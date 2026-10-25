import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Plus, Trash2, Edit2, Check, X } from 'lucide-react'
import { DragDropProvider } from '@dnd-kit/react'
import { useSortable } from '@dnd-kit/react/sortable'
import * as api from '../api/client'
import { FetchErrorRegion } from '../components/FetchErrorRegion'
import { useToast } from '../hooks/useToast'
import { RouteReorderHandle } from '../components/RouteEditor'
import { DeleteConfirmDialog } from '../components/DeleteConfirmDialog'
import { shortRouteId, routeLabel } from '../lib/route'
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

interface RouteRowProps {
  route: Route
  index: number
  total: number
  onMove: (from: number, to: number) => void
  onToggle: (route: Route) => void
  onDelete: (route: Route) => void
  rowRef?: (el: HTMLTableRowElement | null) => void
}

/**
 * Index of a route row within the rendered list, read from the DOM. dnd-kit
 * moves rows in the DOM while dragging and leaves them in the dropped order,
 * so this is the position the user saw at the moment the drop happened.
 */
function domRowIndex(element: Element | null | undefined): number {
  const row = element?.closest('[data-route-row]')
  const body = row?.closest('tbody')
  if (!row || !body) return -1
  const rows = Array.from(body.querySelectorAll<HTMLElement>(':scope > [data-route-row]:not([data-dnd-placeholder])'))
  return rows.indexOf(row as HTMLTableRowElement)
}

function RouteRow({ route, index, total, onMove, onToggle, onDelete, rowRef }: RouteRowProps) {
  const navigate = useNavigate()
  const { ref, handleRef, isDragging, isDropTarget } = useSortable({ id: route.id, index })
  const label = routeLabel(route.name, route.id)
  const classes = ['route-row']
  if (isDragging) classes.push('route-row--dragging')
  if (isDropTarget) classes.push('route-row--drop-target')

  return (
    <tr
      ref={(el) => {
        ref(el)
        rowRef?.(el)
      }}
      className={classes.join(' ')}
      tabIndex={-1}
      data-route-row
    >
      <td data-label="Order">
        <RouteReorderHandle name={label} index={index} total={total} onMove={onMove} handleRef={handleRef} />
      </td>
      <td data-label="Name">
        <Link to={`/routes/${route.id}`}>
          {route.name} <span className="route-short-id">({shortRouteId(route.id)})</span>
        </Link>
        <p style={{ margin: 0, color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>{route.description}</p>
      </td>
      <td data-label="Conditions">
        <span style={{ color: (route.conditions ?? []).length === 0 ? 'var(--color-text-muted)' : undefined }}>
          {conditionSummary(route)}
        </span>
      </td>
      <td data-label="Enabled">
        <label
          style={{ display: 'inline-flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}
          aria-label={route.is_enabled ? `Disable route ${label}` : `Enable route ${label}`}
        >
          <span className="switch">
            <span className="switch-track" aria-hidden="true" />
            <input
              type="checkbox"
              role="switch"
              aria-checked={route.is_enabled}
              checked={route.is_enabled}
              onChange={() => onToggle(route)}
            />
            <span className="switch-thumb" aria-hidden="true">
              {route.is_enabled ? <Check size={12} /> : <X size={12} />}
            </span>
          </span>
          <span style={{ color: route.is_enabled ? 'var(--color-status-accepted)' : 'var(--color-status-rejected)', fontWeight: 500 }}>
            {route.is_enabled ? 'Enabled' : 'Disabled'}
          </span>
        </label>
      </td>
      <td data-label="Final">{route.is_final && <span className="badge badge--accepted">Final</span>}</td>
      <td data-label="Matches">{route.match_count ?? '—'}</td>
      <td data-label="Actions">
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="icon-button" onClick={() => navigate(`/routes/${route.id}`)} aria-label={`Edit route ${label}`}>
            <Edit2 size={16} />
          </button>
          <button
            className="icon-button"
            onClick={() => onDelete(route)}
            aria-label={`Delete route ${label}`}
          >
            <Trash2 size={16} />
          </button>
        </div>
      </td>
    </tr>
  )
}

export function RoutesView() {
  const [routes, setRoutes] = useState<Route[]>([])
  const [loading, setLoading] = useState(true)
  const [fetchError, setFetchError] = useState(false)
  const [retrying, setRetrying] = useState(false)
  const [deletingRoute, setDeletingRoute] = useState<Route | null>(null)
  const retryButtonRef = useRef<HTMLButtonElement>(null)
  const firstRowRef = useRef<HTMLTableRowElement>(null)
  const emptyActionRef = useRef<HTMLAnchorElement>(null)
  const recoveredRef = useRef(false)
  const focusRowAfterDeleteRef = useRef<number | null>(null)
  const { addToast } = useToast()

  useEffect(() => {
    let cancelled = false
    api.listRoutes()
      .then((r) => {
        if (cancelled) return
        setRoutes(r)
        setFetchError(false)
        setLoading(false)
      })
      .catch(() => {
        if (cancelled) return
        setFetchError(true)
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  // Focus the Retry button whenever the view settles into the fetch-error
  // state: on first entry and again after a failed retry re-enables it.
  useEffect(() => {
    if (fetchError && !retrying) retryButtonRef.current?.focus()
  }, [fetchError, retrying])

  // After a successful retry, move focus to the first route row (or the
  // "New Route" action when the list is empty).
  useEffect(() => {
    if (!recoveredRef.current || loading || fetchError) return
    recoveredRef.current = false
    const target = firstRowRef.current ?? emptyActionRef.current
    target?.focus()
  })

  // After a delete, focus moves to the row that took the deleted route's place
  // (or the last row when the deleted route was last).
  useEffect(() => {
    const target = focusRowAfterDeleteRef.current
    if (target === null || loading || fetchError) return
    focusRowAfterDeleteRef.current = null
    const rows = document.querySelectorAll<HTMLTableRowElement>('[data-route-row]')
    const next = rows[Math.min(target, rows.length - 1)] ?? emptyActionRef.current
    next?.focus()
  })

  async function handleRetry() {
    setRetrying(true)
    try {
      const r = await api.listRoutes()
      setRoutes(r)
      setFetchError(false)
      recoveredRef.current = true
    } catch {
      addToast('Retry failed. Please try again.', 'error')
    } finally {
      setRetrying(false)
    }
  }

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
    const deletedIndex = routes.findIndex((r) => r.id === route.id)
    try {
      await api.deleteRoute(route.id)
      if (deletedIndex >= 0) focusRowAfterDeleteRef.current = deletedIndex
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
        {fetchError ? (
          // Toolbar stays visible but non-functional until a retry succeeds.
          <button type="button" className="button button--primary" disabled>
            <Plus size={16} aria-hidden="true" /> New Route
          </button>
        ) : (
          <Link to="/routes/new" className="button button--primary">
            <Plus size={16} aria-hidden="true" /> New Route
          </Link>
        )}
      </div>
      {fetchError ? (
        <FetchErrorRegion
          ref={retryButtonRef}
          message="Failed to load routes. Check your connection and try again."
          onRetry={handleRetry}
          retrying={retrying}
        />
      ) : routes.length === 0 ? (
        <div className="card empty-state">
          <p>No routes yet. Create your first route to start routing videos.</p>
          <Link to="/routes/new" className="button button--primary" style={{ marginTop: 16 }} ref={emptyActionRef}>
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
            <DragDropProvider
              onDragEnd={(event) => {
                // Escape (or a canceled sensor) restores the previous order
                // without persisting anything.
                if (event.canceled) return
                const { source } = event.operation
                if (!source) return
                const from = routes.findIndex((r) => r.id === source.id)
                if (from === -1) return
                // dnd-kit leaves the list in the order it optimistically showed
                // while dragging, so the dropped row's position in the DOM is
                // the order the user just saw and asked us to save.
                const to = domRowIndex(source.element)
                if (to === -1 || to === from) return
                move(from, to)
              }}
            >
              <tbody>
                {routes.map((r, i) => (
                  <RouteRow
                    key={r.id}
                    route={r}
                    index={i}
                    total={routes.length}
                    onMove={move}
                    onToggle={toggleEnabled}
                    onDelete={setDeletingRoute}
                    rowRef={i === 0 ? (el) => { firstRowRef.current = el } : undefined}
                  />
                ))}
              </tbody>
            </DragDropProvider>
          </table>
        </div>
      )}
      {deletingRoute && (
        <DeleteConfirmDialog
          routeLabel={routeLabel(deletingRoute.name, deletingRoute.id)}
          onCancel={() => setDeletingRoute(null)}
          onConfirm={() => confirmDelete(deletingRoute)}
        />
      )}
    </>
  )
}
