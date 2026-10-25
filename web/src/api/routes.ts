import type { Route } from '../types'
import { api, ApiError } from './core'

function normalizeRoute(route: Route): Route {
  return {
    ...route,
    conditions: route.conditions ?? [],
    actions: route.actions ?? [],
    exclusions: route.exclusions ?? [],
  }
}

// ARS-004: create requests MUST omit any route id — the server assigns the
// UUID. Strip identity/timestamp fields so a full Route object can be passed
// through without violating the contract.
function routePayload(route: Omit<Route, 'id' | 'created_at' | 'updated_at'>): unknown {
  const { id: _id, created_at: _created, updated_at: _updated, ...payload } = route as Route
  return payload
}

export async function listRoutes(): Promise<Route[]> {
  const data = await api<{ routes: Route[] }>('/api/routes')
  // A missing or non-array collection violates the API contract (ARS-001);
  // surface it as a failure instead of normalizing it to an empty list.
  if (!data || !Array.isArray(data.routes)) {
    throw new Error('Invalid response from /api/routes: routes field missing or not an array')
  }
  return data.routes
    .map(normalizeRoute)
    .sort((a, b) => a.evaluation_order - b.evaluation_order)
}

export async function getRoute(id: string): Promise<Route | undefined> {
  try {
    const route = await api<Route>(`/api/routes/${id}`)
    return normalizeRoute(route)
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
    body: JSON.stringify(routePayload(route)),
  })
}

export async function updateRoute(id: string, route: Omit<Route, 'id' | 'created_at' | 'updated_at'>): Promise<Route> {
  return api<Route>(`/api/routes/${id}`, {
    method: 'PUT',
    body: JSON.stringify(routePayload(route)),
  })
}

export async function deleteRoute(id: string): Promise<void> {
  await api<void>(`/api/routes/${id}`, { method: 'DELETE' })
}

export async function reorderRoutes(ids: string[]): Promise<void> {
  await api<void>('/api/routes/reorder', {
    method: 'PUT',
    body: JSON.stringify({ route_ids: ids }),
  })
}
