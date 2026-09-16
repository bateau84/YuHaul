const API_BASE = ''

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = 'ApiError'
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: 'request failed' }))
    throw new ApiError(res.status, body.error ?? `HTTP ${res.status}`)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return res.json() as Promise<T>
}
