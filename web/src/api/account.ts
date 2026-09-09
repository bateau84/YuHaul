import { api } from './core'

export async function getAccountStatus(): Promise<{ connected: boolean; email?: string }> {
  return api('/api/account/status')
}

export async function disconnectAccount(): Promise<void> {
  await api<void>('/api/account/', { method: 'DELETE' })
}

export async function initiateOAuth(): Promise<{ auth_url: string; state: string }> {
  return api<{ auth_url: string; state: string }>('/api/auth/initiate', { method: 'POST' })
}
