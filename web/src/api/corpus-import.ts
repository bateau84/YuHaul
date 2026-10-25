import type { CorpusImportMetadata, CorpusImportPreview, CorpusImportStart, CorpusImportStatus } from '../types'
import { ApiError, api } from './core'

// Direct SQLite corpus import client (ARS-002). The backend endpoints are not
// implemented yet; these call the contract paths directly (no mock fallback)
// so a missing or failing backend surfaces as an error instead of fabricated
// data. Component tests mock at the fetch layer via src/test/setup.ts.

// Multipart upload init: exactly one file part named `file`. No Content-Type
// header is set — the browser must add the multipart boundary for FormData,
// and forcing application/json would break the upload.
function uploadInit(file: File): RequestInit {
  const body = new FormData()
  body.append('file', file)
  return { method: 'POST', body, headers: {} }
}

// POST /api/corpus/import/preview — read-only preview; no destination writes.
export function previewCorpusImport(file: File): Promise<CorpusImportPreview> {
  return api<CorpusImportPreview>('/api/corpus/import/preview', uploadInit(file))
}

// POST /api/corpus/import — accepts the same file again and starts the
// append-only import (202 with an import id).
export function startCorpusImport(file: File): Promise<CorpusImportStart> {
  return api<CorpusImportStart>('/api/corpus/import', uploadInit(file))
}

// GET /api/corpus/import/{import_id} — current in-process state. A 404 means
// the id is unknown, including a job lost to a process restart; it propagates
// as ApiError(404) so callers can tell status loss from transient failure.
export function getCorpusImportStatus(importId: string): Promise<CorpusImportStatus> {
  return api<CorpusImportStatus>(`/api/corpus/import/${encodeURIComponent(importId)}`)
}

// GET /api/corpus/import — active or most recent terminal operation in this
// process; 404 (no state, e.g. after a restart) maps to null.
export async function getLatestCorpusImport(): Promise<CorpusImportStatus | null> {
  try {
    return await api<CorpusImportStatus>('/api/corpus/import')
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null
    throw err
  }
}

// GET /api/corpus/import/metadata — durable recency of the last successful
// import (ARS-002). Independent of the process-local job manager and readable
// after a restart; `last_successful_corpus_import_at` is null when no reliable
// completion timestamp is recorded. Server failures (500) propagate as
// ApiError so callers can show a fetch error distinct from a recorded null.
// The literal `/metadata` segment is a reserved route, never an import id.
export function getCorpusImportMetadata(): Promise<CorpusImportMetadata> {
  return api<CorpusImportMetadata>('/api/corpus/import/metadata')
}

// POST /api/corpus/import/{import_id}/cancel — explicit cancellation only;
// leaving the page never cancels. Unknown id propagates ApiError(404).
export function cancelCorpusImport(importId: string): Promise<CorpusImportStatus> {
  return api<CorpusImportStatus>(`/api/corpus/import/${encodeURIComponent(importId)}/cancel`, {
    method: 'POST',
  })
}
