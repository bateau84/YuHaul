import type { Run } from '../types'
import { api } from './core'

export interface Overview {
  last_run: Run | null
  runs_today: number
  quota: {
    daily_limit: number
    consumed_today: number
    remaining: number
    reset_at?: string
  }
  error_count: number
  deferred_count: number
}

export async function getOverview(): Promise<Overview> {
  return api<Overview>('/api/overview')
}
