import type { Route } from '../types'

export function blankRoute(evaluationOrder = 1): Route {
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
