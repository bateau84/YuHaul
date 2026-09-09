import { api } from './core'

export async function getAccountStatus(): Promise<{ connected: boolean; email?: string }> {
  return api('/api/account/status')
}

export async function disconnectAccount(): Promise<void> {
  await api<void>('/api/account/', { method: 'DELETE' })
}
