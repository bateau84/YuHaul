const API_BASE = ''

export class ApiError extends Error {
  status: number
  // Closed machine-readable fields some refusals carry next to the message
  // (for example Run Now's resume_unavailable: code, run_id, reason).
  code?: string
  runId?: number
  reason?: string
  constructor(status: number, message: string, details?: { code?: string; runId?: number; reason?: string }) {
    super(message)
    this.status = status
    this.name = 'ApiError'
    this.code = details?.code
    this.runId = details?.runId
    this.reason = details?.reason
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  if (!res.ok) {
    const body = await res.json().catch(() => ({ error: 'request failed' }))
    throw new ApiError(res.status, body.error ?? `HTTP ${res.status}`, {
      code: typeof body.code === 'string' ? body.code : undefined,
      runId: typeof body.run_id === 'number' ? body.run_id : undefined,
      reason: typeof body.reason === 'string' ? body.reason : undefined,
    })
  }
  if (res.status === 204) {
    return undefined as T
  }
  return res.json() as Promise<T>
}
