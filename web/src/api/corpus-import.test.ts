import { describe, it, expect, vi, afterEach } from 'vitest'
import {
  previewCorpusImport,
  startCorpusImport,
  getCorpusImportStatus,
  getLatestCorpusImport,
  getCorpusImportMetadata,
  cancelCorpusImport,
} from './corpus-import'
import { ApiError } from './core'
import type { CorpusImportStatus } from '../types'

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

// Extracts the multipart `file` part from a captured fetch init, or null when
// the request body is not the expected FormData.
function filePart(init: RequestInit | undefined): FormDataEntryValue | null {
  const body = init?.body
  if (!(body instanceof FormData)) return null
  return body.get('file')
}

const RUNNING_STATUS: CorpusImportStatus = {
  import_id: 'job-1',
  status: 'running',
  total: 12000,
  processed: 3200,
  inserted: 2980,
  skipped: 190,
  errors: 30,
  error_counts_by_reason: { row_read_failed: 30 },
  rate_per_second: 180,
  estimated_seconds_remaining: 49,
}

describe('corpus import API client (ARS-002)', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('uploads the selected file as the single multipart part named file, without forcing a JSON content type', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      jsonResponse({ source_row_count: 12000, already_present_count: 847, sample_titles: ['Example title'] }),
    )
    const file = new File(['sqlite'], 'corpus.db')

    const preview = await previewCorpusImport(file)

    expect(preview.source_row_count).toBe(12000)
    expect(preview.already_present_count).toBe(847)
    expect(fetchSpy).toHaveBeenCalledWith('/api/corpus/import/preview', expect.anything())
    const init = fetchSpy.mock.calls[0][1]
    expect(init?.method).toBe('POST')
    expect(init?.body).toBeInstanceOf(FormData)
    expect(filePart(init)).toBe(file)
    expect(init?.headers ?? {}).not.toHaveProperty('Content-Type')
  })

  it('re-submits the file to POST /api/corpus/import and returns the accepted import id', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(jsonResponse({ import_id: 'job-1', status: 'queued' }, 202))
    const file = new File(['sqlite'], 'corpus.sqlite')

    await expect(startCorpusImport(file)).resolves.toEqual({ import_id: 'job-1', status: 'queued' })
    expect(fetchSpy).toHaveBeenCalledWith('/api/corpus/import', expect.anything())
    expect(filePart(fetchSpy.mock.calls[0][1])).toBe(file)
  })

  it('reads and cancels status by import id', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse(RUNNING_STATUS))

    await expect(getCorpusImportStatus('job-1')).resolves.toEqual(RUNNING_STATUS)
    expect(fetchSpy).toHaveBeenLastCalledWith('/api/corpus/import/job-1', expect.anything())

    const cancelRequested: CorpusImportStatus = { ...RUNNING_STATUS, status: 'cancel_requested' }
    fetchSpy.mockResolvedValue(jsonResponse(cancelRequested, 202))
    await expect(cancelCorpusImport('job-1')).resolves.toEqual(cancelRequested)
    expect(fetchSpy).toHaveBeenLastCalledWith('/api/corpus/import/job-1/cancel', expect.anything())
    expect(fetchSpy.mock.calls[1][1]?.method).toBe('POST')
  })

  it('maps the collection 404 (no process state) to null and keeps other failures as errors', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValue(jsonResponse({ error: 'no import state available' }, 404))

    await expect(getLatestCorpusImport()).resolves.toBeNull()

    fetchSpy.mockResolvedValue(jsonResponse({ error: 'internal error' }, 500))
    await expect(getLatestCorpusImport()).rejects.toBeInstanceOf(ApiError)
  })

  it('keeps a per-id 404 (job lost to a process restart) as a typed 404 error', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ error: 'import not found' }, 404))

    await expect(getCorpusImportStatus('lost-job')).rejects.toMatchObject({ status: 404 })
    await expect(cancelCorpusImport('lost-job')).rejects.toMatchObject({ status: 404 })
  })

  it('reads the reserved metadata route, returning the durable timestamp or a recorded null', async () => {
    const fetchSpy = vi
      .spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ last_successful_corpus_import_at: '2026-10-24T12:34:56.123456789Z' }))
      .mockResolvedValueOnce(jsonResponse({ last_successful_corpus_import_at: null }))

    await expect(getCorpusImportMetadata()).resolves.toEqual({
      last_successful_corpus_import_at: '2026-10-24T12:34:56.123456789Z',
    })
    expect(fetchSpy).toHaveBeenNthCalledWith(1, '/api/corpus/import/metadata', expect.anything())

    // A recorded null is a 200 body value, not an error.
    await expect(getCorpusImportMetadata()).resolves.toEqual({ last_successful_corpus_import_at: null })
    expect(fetchSpy).toHaveBeenNthCalledWith(2, '/api/corpus/import/metadata', expect.anything())

    // A failed read stays an error, distinct from a recorded null.
    fetchSpy.mockResolvedValue(jsonResponse({ error: 'failed to read corpus import metadata' }, 500))
    await expect(getCorpusImportMetadata()).rejects.toBeInstanceOf(ApiError)
  })
})
