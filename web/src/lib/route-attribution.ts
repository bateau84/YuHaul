import type { RouteDecisionSummary } from '../types'

export function routeAttribution(decision: RouteDecisionSummary): string {
  const id = decision.route_id
  if (decision.route_name && decision.route_name_source === 'run_snapshot') return `Route: ${decision.route_name} (ID: ${id})`
  if (decision.route_name && decision.route_name_source === 'current_local') return `Route: ${decision.route_name} (current/local name; ID: ${id})`
  return `Route name unavailable (ID: ${id})`
}
