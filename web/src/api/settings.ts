import type { ConfigVersion, Settings } from '../types'
import { api } from './core'

export async function listConfigVersions(): Promise<ConfigVersion[]> {
  const data = await api<{ versions: ConfigVersion[] }>('/api/config/versions')
  return data.versions
}

export async function getConfigVersion(id: number): Promise<ConfigVersion | undefined> {
  try {
    return await api<ConfigVersion>(`/api/config/versions/${id}`)
  } catch (err) {
    if (err instanceof Error && err.message.includes('not found')) {
      return undefined
    }
    throw err
  }
}

export async function getSettings(): Promise<Settings> {
  return api<Settings>('/api/settings')
}

export async function updateSettings(settings: Partial<Settings>): Promise<Settings> {
  return api<Settings>('/api/settings', {
    method: 'PUT',
    body: JSON.stringify(settings),
  })
}

export async function getDuplicateThreshold(): Promise<{
  threshold: number
  default_threshold: number
  duplicate_behavior: string
  range: { min: number; max: number }
}> {
  return api('/api/settings/duplicate-threshold')
}

export async function setDuplicateThreshold(threshold: number): Promise<void> {
  await api<void>('/api/settings/duplicate-threshold', {
    method: 'PUT',
    body: JSON.stringify({ threshold }),
  })
}
