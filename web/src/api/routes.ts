import type { Route } from '../types'
import { api, ApiError } from './core'

export async function listRoutes(): Promise<Route[]> {
  const data = await api<{ routes: Route[] }>('/api/routes')
  return (data.routes ?? []).sort((a, b) => a.evaluation_order - b.evaluation_order)
}

export async function getRoute(id: number): Promise<Route | undefined> {
  try {
    return await api<Route>(`/api/routes/${id}`)
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return undefined
    }
    throw err
  }
}

export async function createRoute(route: Omit<Route, 'id' | 'created_at' | 'updated_at'>): Promise<Route> {
  return api<Route>('/api/routes', {
    method: 'POST',
    body: JSON.stringify(route),
  })
}

export async function updateRoute(id: number, route: Omit<Route, 'id' | 'created_at' | 'updated_at'>): Promise<Route> {
  return api<Route>(`/api/routes/${id}`, {
    method: 'PUT',
    body: JSON.stringify(route),
  })
}

export async function deleteRoute(id: number): Promise<void> {
  await api<void>(`/api/routes/${id}`, { method: 'DELETE' })
}

export async function reorderRoutes(ids: number[]): Promise<void> {
  await api<void>('/api/routes/reorder', {
    method: 'PUT',
    body: JSON.stringify({ route_ids: ids }),
  })
}

export async function getRouteStats(id: number): Promise<{ match_count: number }> {
  return api<{ match_count: number }>(`/api/routes/${id}/stats`)
}
