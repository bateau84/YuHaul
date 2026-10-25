import type { Route } from '../types'

// A route that has not been saved yet has no identity: the server assigns the
// UUID on create (ARS-004), and create requests must omit any route id.
export function blankRoute(evaluationOrder = 1): Route {
  return {
    id: '',
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

export function normalizeRoute(route: Route): Route {
  return {
    ...route,
    conditions: route.conditions ?? [],
    actions: route.actions ?? [],
    exclusions: route.exclusions ?? [],
  }
}

/**
 * Short UUID suffix used wherever route identity matters: the first 8 hex
 * characters of the canonical UUIDv4 (accepted Route Identity design
 * amendment). Always shown, not only for duplicate names, so the list does not
 * re-flow and historical references stay unambiguous.
 */
export function shortRouteId(id: string): string {
  return id.replace(/-/g, '').slice(0, 8)
}

/** Disambiguated route label: `Name (a3f5b2c1)`. */
export function routeLabel(name: string, id: string): string {
  return id ? `${name} (${shortRouteId(id)})` : name
}
