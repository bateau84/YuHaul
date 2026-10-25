import { describe, it, expect, vi, beforeAll, afterAll, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { SettingsView } from './SettingsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import { ApiError } from '../api/core'
import type { CorpusImportPreview, CorpusImportStatus } from '../types'

const IMPORT_ID_KEY = 'yuhaul.corpus_import_id'

// Exact accepted null-state copy (design amendment: Settings corpus import
// recency display). Kept verbatim here so a copy regression fails the test.
const RECENCY_NULL_VALUE = 'Not recorded'
const RECENCY_NULL_HELPER =
  'No reliable import date is available. This can occur when the database predates import tracking. Run a successful import to establish a timestamp.'

const PREVIEW: CorpusImportPreview = {
  source_row_count: 12000,
  already_present_count: 847,
  sample_titles: ['Example title one', 'Example title two'],
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

const CANCEL_REQUESTED_STATUS: CorpusImportStatus = {
  ...RUNNING_STATUS,
  status: 'cancel_requested',
}

const COMPLETED_STATUS: CorpusImportStatus = {
  import_id: 'job-1',
  status: 'completed',
  total: 12000,
  processed: 12000,
  inserted: 11153,
  skipped: 847,
  errors: 0,
  error_counts_by_reason: {},
  rate_per_second: 180,
  estimated_seconds_remaining: null,
  total_corpus_size: 12000,
}

function renderWithToast(ui: React.ReactElement) {
  return render(<ToastProvider>{ui}</ToastProvider>)
}

async function renderSettingsWithPicker() {
  const view = renderWithToast(<SettingsView />)
  await screen.findByLabelText('SQLite database file')
  return view
}

async function selectCorpusFile() {
  const input = document.querySelector('#corpus-import-file') as HTMLInputElement
  fireEvent.change(input, { target: { files: [new File(['sqlite'], 'corpus.db')] } })
  await screen.findByRole('button', { name: 'Start import' })
}

describe('SettingsView', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/settings')
    sessionStorage.clear()
  })

  it('explains historical-corpus duplicate matching without implying video ID self-matches', async () => {
    renderWithToast(<SettingsView />)

    expect(await screen.findByText('Only different videos in the historical corpus can be duplicate matches. Exact title matches do not depend on this threshold; fuzzy title matches use it. A video never matches itself by video ID.')).toBeInTheDocument()
    expect(screen.getByRole('slider', { name: /Similarity threshold/ })).toBeInTheDocument()
  })

  it('explains fixed continuation immediately after matching guidance without adding a handling control', async () => {
    const save = vi.spyOn(mock, 'setDuplicateThreshold')
    renderWithToast(<SettingsView />)
    const explanation = await screen.findByText('Duplicate matches do not stop routing. Videos continue through eligible routes and playlist actions; exclusions, route conditions and order, final-route rules, quota and failure handling, and completed-action checks still apply.')
    expect(explanation.previousElementSibling).toHaveTextContent('A video never matches itself by video ID.')
    const card = explanation.closest('.card')!
    expect(card.querySelectorAll('input, select, button')).toHaveLength(1)
    expect(explanation).not.toHaveAttribute('tabindex')
    expect(explanation).not.toHaveAttribute('role')
    const threshold = screen.getByRole('slider', { name: /Similarity threshold/ })
    fireEvent.change(threshold, { target: { value: '0.75' } })
    fireEvent.blur(threshold)
    await waitFor(() => expect(save).toHaveBeenCalledWith(0.75))
  })

  it('shows connect button when account is not connected', async () => {
    vi.spyOn(mock, 'getSettings').mockResolvedValueOnce({
      schedule: '0 6 * * *',
      quota_limit: 10000,
      duplicate_threshold: 0.5,
      duplicate_behavior: 'skip',
      account_connected: false,
      account_email: undefined,
    })

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(screen.getByRole('button', { name: /Connect YouTube account/i })).toBeInTheDocument())
  })

  it('shows success toast and refreshes status when auth=connected query param is present', async () => {
    window.history.replaceState({}, '', '/settings?auth=connected')

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/YouTube account connected/i))

    // Query parameter should be stripped.
    expect(window.location.search).toBe('')
  })

  it('shows error state when settings fetch fails', async () => {
    vi.spyOn(mock, 'getSettings').mockRejectedValueOnce(new Error('Server error'))

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(screen.getByText(/Server error/i)).toBeInTheDocument())

    expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()
  })

  it('initiates OAuth flow when connect button is clicked', async () => {
    vi.spyOn(mock, 'getSettings').mockResolvedValueOnce({
      schedule: '0 6 * * *',
      quota_limit: 10000,
      duplicate_threshold: 0.5,
      duplicate_behavior: 'skip',
      account_connected: false,
      account_email: undefined,
    })

    const assignMock = vi.fn()
    const originalLocation = window.location
    // @ts-expect-error jsdom location is not configurable; replace for the test.
    delete window.location
    window.location = { ...originalLocation, assign: assignMock } as unknown as Location

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(screen.getByRole('button', { name: /Connect YouTube account/i })).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Connect YouTube account/i }))
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://accounts.google.com/oauth/mock'))

    window.location = originalLocation
  })
})

describe('SettingsView historical corpus import', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/settings')
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('previews a selected SQLite file with truthful metadata and no date range', async () => {
    let resolvePreview!: (preview: CorpusImportPreview) => void
    vi.spyOn(mock, 'previewCorpusImport').mockReturnValue(
      new Promise<CorpusImportPreview>((resolve) => {
        resolvePreview = resolve
      }),
    )

    await renderSettingsWithPicker()
    const input = document.querySelector('#corpus-import-file') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['sqlite'], 'corpus.db')] } })

    // Loading state while the preview request is in flight.
    await screen.findByText('Reading preview…')

    await act(async () => {
      resolvePreview(PREVIEW)
    })

    expect(await screen.findByText(`${(12000).toLocaleString()} titles found in source database.`)).toBeInTheDocument()
    expect(
      screen.getByText(`${(847).toLocaleString()} video identifiers already exist in corpus (will be skipped).`),
    ).toBeInTheDocument()
    expect(screen.getByText('First 2 sample titles shown for confirmation:')).toBeInTheDocument()
    expect(screen.getByText('Example title one')).toBeInTheDocument()
    expect(screen.getByText('Example title two')).toBeInTheDocument()

    // Honest preview: no date range, no Export, no CSV/JSON, no append-mode choice.
    expect(screen.queryByText(/date range/i)).toBeNull()
    expect(screen.queryByRole('button', { name: /export/i })).toBeNull()
    expect(screen.queryByText(/csv|json/i)).toBeNull()
    expect(screen.queryByRole('radio')).toBeNull()
    expect(screen.getByRole('button', { name: 'Start import' })).toBeInTheDocument()
  })

  it('starts the append import, shows progress with rate and ETA, and says leaving does not cancel', async () => {
    vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
    vi.spyOn(mock, 'startCorpusImport').mockResolvedValue({ import_id: 'job-1', status: 'queued' })
    vi.spyOn(mock, 'getCorpusImportStatus').mockResolvedValue(RUNNING_STATUS)

    await renderSettingsWithPicker()
    await selectCorpusFile()
    fireEvent.click(screen.getByRole('button', { name: 'Start import' }))

    expect(await screen.findByText('Importing…')).toBeInTheDocument()
    const progress = screen.getByRole('progressbar')
    expect(progress).toHaveAttribute('max', '12000')
    expect(progress).toHaveAttribute('value', '3200')
    expect(
      screen.getByText('Importing: 3,200 of 12,000 titles (27%) — ~180 titles/sec — estimated 49 seconds remaining'),
    ).toBeInTheDocument()
    expect(screen.getByText('2,980 inserted · 190 already present · 30 failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancel import' })).toBeInTheDocument()
    expect(screen.getByText(/Leaving Settings does not cancel the import/)).toBeInTheDocument()
    expect(sessionStorage.getItem(IMPORT_ID_KEY)).toBe('job-1')
  })

  it('polls to completion and shows the reconciled summary with the actual destination total', async () => {
    vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
    vi.spyOn(mock, 'startCorpusImport').mockResolvedValue({ import_id: 'job-1', status: 'queued' })
    vi.spyOn(mock, 'getCorpusImportStatus')
      .mockResolvedValueOnce(RUNNING_STATUS) // immediate read after acceptance
      .mockResolvedValue(COMPLETED_STATUS) // subsequent polls

    await renderSettingsWithPicker()
    await selectCorpusFile()
    fireEvent.click(screen.getByRole('button', { name: 'Start import' }))
    expect(await screen.findByText('Importing…')).toBeInTheDocument()

    expect(await screen.findByText('Import complete', {}, { timeout: 4000 })).toBeInTheDocument()
    expect(screen.getByText('11,153 newly inserted, 847 already present, 0 failed.')).toBeInTheDocument()
    expect(screen.getByText('Total corpus: 12,000 titles (actual destination row count after import)')).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(screen.getByRole('button', { name: 'Import another file' })).toBeInTheDocument()

    // SC-004 completion toast (rendered in the visible and sr-only regions).
    await waitFor(() =>
      expect(document.querySelector('.toast')).toHaveTextContent(
        'Imported 11,153 new historical titles (847 already present, 0 errors). Total corpus: 12,000 titles.',
      ),
    )
  })

  it('requests explicit cancellation and shows the pending cancel state', async () => {
    vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
    vi.spyOn(mock, 'startCorpusImport').mockResolvedValue({ import_id: 'job-1', status: 'queued' })
    vi.spyOn(mock, 'getCorpusImportStatus')
      .mockResolvedValueOnce(RUNNING_STATUS)
      .mockResolvedValue(CANCEL_REQUESTED_STATUS)
    vi.spyOn(mock, 'cancelCorpusImport').mockResolvedValue(CANCEL_REQUESTED_STATUS)

    await renderSettingsWithPicker()
    await selectCorpusFile()
    fireEvent.click(screen.getByRole('button', { name: 'Start import' }))
    expect(await screen.findByText('Importing…')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Cancel import' }))

    expect(await screen.findByText(/Cancelling — stopping at the next committed chunk/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Cancelling…' })).toBeDisabled()
    expect(mock.cancelCorpusImport).toHaveBeenCalledWith('job-1')
    expect(screen.getByText(/Leaving Settings does not cancel the import/)).toBeInTheDocument()
  })

  it('recovers an in-process import after navigation or reload', async () => {
    sessionStorage.setItem(IMPORT_ID_KEY, 'job-1')
    vi.spyOn(mock, 'getLatestCorpusImport').mockResolvedValue(RUNNING_STATUS)

    renderWithToast(<SettingsView />)

    expect(await screen.findByText('Importing…')).toBeInTheDocument()
    expect(mock.getLatestCorpusImport).toHaveBeenCalled()
    expect(screen.getByRole('button', { name: 'Cancel import' })).toBeInTheDocument()
    expect(screen.getByText(/Leaving Settings does not cancel the import/)).toBeInTheDocument()
    expect(sessionStorage.getItem(IMPORT_ID_KEY)).toBe('job-1')
  })

  it('shows a neutral ready state with no fabricated summary when the stored job was lost to a restart', async () => {
    sessionStorage.setItem(IMPORT_ID_KEY, 'job-1')
    vi.spyOn(mock, 'getLatestCorpusImport').mockResolvedValue(null)
    vi.spyOn(mock, 'getCorpusImportStatus').mockResolvedValue(undefined) // 404: unknown id

    renderWithToast(<SettingsView />)

    expect(await screen.findByText(/Import status is unavailable because the server restarted/)).toBeInTheDocument()
    expect(screen.getByLabelText('SQLite database file')).toBeInTheDocument()
    expect(screen.queryByRole('progressbar')).toBeNull()
    expect(screen.queryByText(/Total corpus/)).toBeNull()
    expect(screen.queryByText('Import complete')).toBeNull()
    expect(sessionStorage.getItem(IMPORT_ID_KEY)).toBeNull()
  })

  it('surfaces an invalid-schema preview error without offering success', async () => {
    vi.spyOn(mock, 'previewCorpusImport').mockRejectedValue(
      new ApiError(400, 'Expected SQLite database with exactly one table containing video_id and title columns.'),
    )

    await renderSettingsWithPicker()
    const input = document.querySelector('#corpus-import-file') as HTMLInputElement
    fireEvent.change(input, { target: { files: [new File(['sqlite'], 'corpus.db')] } })

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Expected SQLite database with exactly one table containing video_id and title columns.')
    expect(screen.queryByRole('button', { name: 'Start import' })).toBeNull()
    expect(screen.queryByRole('progressbar')).toBeNull()
    // The file stays selected so the upload can be retried under append semantics.
    expect(screen.getByRole('button', { name: 'Retry preview' })).toBeInTheDocument()
  })

  it('surfaces a rejected start and keeps the preview available for retry', async () => {
    vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
    vi.spyOn(mock, 'startCorpusImport').mockRejectedValue(new ApiError(409, 'an import is already active'))

    await renderSettingsWithPicker()
    await selectCorpusFile()
    fireEvent.click(screen.getByRole('button', { name: 'Start import' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('an import is already active')
    expect(screen.getByRole('button', { name: 'Start import' })).toBeInTheDocument()
    expect(screen.getByText(`${(12000).toLocaleString()} titles found in source database.`)).toBeInTheDocument()
    expect(sessionStorage.getItem(IMPORT_ID_KEY)).toBeNull()
  })

  it('reports a status read failure with Retry and recovers', async () => {
    vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
    vi.spyOn(mock, 'startCorpusImport').mockResolvedValue({ import_id: 'job-1', status: 'queued' })
    vi.spyOn(mock, 'getCorpusImportStatus')
      .mockResolvedValueOnce(RUNNING_STATUS) // immediate read after acceptance
      .mockRejectedValueOnce(new Error('network down')) // first poll fails
      .mockResolvedValue(COMPLETED_STATUS) // after Retry

    await renderSettingsWithPicker()
    await selectCorpusFile()
    fireEvent.click(screen.getByRole('button', { name: 'Start import' }))
    expect(await screen.findByText('Importing…')).toBeInTheDocument()

    const alert = await screen.findByRole('alert', {}, { timeout: 4000 })
    expect(alert).toHaveTextContent('Could not read import status: network down')

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Import complete')).toBeInTheDocument()
  })
})

describe('SettingsView corpus import recency', () => {
  const ORIGINAL_TZ = process.env.TZ

  beforeAll(() => {
    // Pin the local timezone so the formatted value is deterministic and the
    // test genuinely distinguishes local-time rendering from a UTC passthrough.
    process.env.TZ = 'America/New_York'
  })

  afterAll(() => {
    if (ORIGINAL_TZ === undefined) delete process.env.TZ
    else process.env.TZ = ORIGINAL_TZ
  })

  beforeEach(() => {
    window.history.replaceState({}, '', '/settings')
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('shows the durable import timestamp in the local timezone, not a substituted date', async () => {
    vi.spyOn(mock, 'getCorpusImportMetadata').mockResolvedValue({
      last_successful_corpus_import_at: '2026-10-24T12:34:56Z',
    })

    renderWithToast(<SettingsView />)

    // 12:34:56Z is 08:34 in America/New_York (EDT, UTC-4) on that date.
    expect(await screen.findByText('October 24, 2026 at 8:34 AM')).toBeInTheDocument()
    expect(screen.getByText('Last corpus import')).toBeInTheDocument()
    expect(screen.queryByText(RECENCY_NULL_VALUE)).toBeNull()
    expect(screen.queryByText(/No reliable import date is available/)).toBeNull()
    expect(screen.queryByText(/12:34/)).toBeNull()
  })

  it('shows the accepted null-state copy, associated and not error-styled, when no timestamp is recorded', async () => {
    vi.spyOn(mock, 'getCorpusImportMetadata').mockResolvedValue({ last_successful_corpus_import_at: null })

    renderWithToast(<SettingsView />)

    const value = await screen.findByText(RECENCY_NULL_VALUE)
    expect(value).toBeInTheDocument()
    // Label/value/helper association for screen readers.
    expect(screen.getByText('Last corpus import')).toBeInTheDocument()
    expect(value).toHaveAttribute('aria-labelledby', 'corpus-recency-label')
    expect(value).toHaveAttribute('aria-describedby', 'corpus-recency-help')
    expect(screen.getByText(RECENCY_NULL_HELPER)).toBeInTheDocument()
    // Informational, not an error state, and it does not claim the corpus was
    // never imported.
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.queryByText(/never imported|no imports|empty/i)).toBeNull()
  })

  it('shows an explicit fetch error distinct from the recorded-null state, with retry', async () => {
    vi.spyOn(mock, 'getCorpusImportMetadata')
      .mockRejectedValueOnce(new ApiError(500, 'failed to read corpus import metadata'))
      .mockResolvedValue({ last_successful_corpus_import_at: null })

    renderWithToast(<SettingsView />)

    expect(await screen.findByText('unavailable')).toBeInTheDocument()
    expect(screen.getByText('Could not read the last import date: failed to read corpus import metadata')).toBeInTheDocument()
    // The fetch failure must not masquerade as a recorded null.
    expect(screen.queryByText(RECENCY_NULL_VALUE)).toBeNull()
    expect(screen.queryByText(RECENCY_NULL_HELPER)).toBeNull()
    // The rest of Settings stays usable.
    expect(screen.getByLabelText('SQLite database file')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText(RECENCY_NULL_VALUE)).toBeInTheDocument()
    expect(screen.queryByText('unavailable')).toBeNull()
  })

  it.each(['polled', 'immediate', 'all-skipped'] as const)(
    'refreshes the displayed recency after successful import completion (%s)',
    async (completion) => {
      vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
      vi.spyOn(mock, 'startCorpusImport').mockResolvedValue({ import_id: 'job-1', status: 'queued' })
      vi.spyOn(mock, 'getCorpusImportStatus')
        .mockResolvedValueOnce(completion === 'polled' ? RUNNING_STATUS : {
          ...COMPLETED_STATUS,
          inserted: completion === 'all-skipped' ? 0 : COMPLETED_STATUS.inserted,
          skipped: completion === 'all-skipped' ? COMPLETED_STATUS.total : COMPLETED_STATUS.skipped,
        })
        .mockResolvedValue(COMPLETED_STATUS)
      vi.spyOn(mock, 'getCorpusImportMetadata')
        .mockResolvedValueOnce({ last_successful_corpus_import_at: null })
        .mockResolvedValue({ last_successful_corpus_import_at: '2026-10-24T12:34:56Z' })

      await renderSettingsWithPicker()
      expect(await screen.findByText(RECENCY_NULL_VALUE)).toBeInTheDocument()

      await selectCorpusFile()
      fireEvent.click(screen.getByRole('button', { name: 'Start import' }))
      expect(await screen.findByText('Import complete', {}, { timeout: 4000 })).toBeInTheDocument()

      // The recency display advances from the durable marker without a reload.
      expect(await screen.findByText('October 24, 2026 at 8:34 AM')).toBeInTheDocument()
      expect(screen.queryByText(RECENCY_NULL_VALUE)).toBeNull()
      expect(mock.getCorpusImportMetadata).toHaveBeenCalledTimes(2)
    },
  )

  it('surfaces a metadata refresh failure after immediate completion and retries without losing the result', async () => {
    vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
    vi.spyOn(mock, 'startCorpusImport').mockResolvedValue({ import_id: 'job-1', status: 'queued' })
    vi.spyOn(mock, 'getCorpusImportStatus').mockResolvedValue(COMPLETED_STATUS)
    vi.spyOn(mock, 'getCorpusImportMetadata')
      .mockResolvedValueOnce({ last_successful_corpus_import_at: null })
      .mockRejectedValueOnce(new Error('metadata unavailable'))
      .mockResolvedValue({ last_successful_corpus_import_at: null })

    await renderSettingsWithPicker()
    await screen.findByText(RECENCY_NULL_VALUE)
    await selectCorpusFile()
    fireEvent.click(screen.getByRole('button', { name: 'Start import' }))

    expect(await screen.findByText('Import complete')).toBeInTheDocument()
    expect(await screen.findByText('unavailable')).toBeInTheDocument()
    expect(screen.getByText('Could not read the last import date: metadata unavailable')).toBeInTheDocument()
    expect(screen.queryByText(RECENCY_NULL_VALUE)).toBeNull()

    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText(RECENCY_NULL_VALUE)).toBeInTheDocument()
    expect(screen.getByText('Import complete')).toBeInTheDocument()
    expect(mock.getCorpusImportMetadata).toHaveBeenCalledTimes(3)
  })

  it.each(['cancelled', 'failed'] as const)('does not refresh recency for a %s import', async (status) => {
    vi.spyOn(mock, 'previewCorpusImport').mockResolvedValue(PREVIEW)
    vi.spyOn(mock, 'startCorpusImport').mockResolvedValue({ import_id: 'job-1', status: 'queued' })
    vi.spyOn(mock, 'getCorpusImportStatus').mockResolvedValue({ ...RUNNING_STATUS, status })
    vi.spyOn(mock, 'getCorpusImportMetadata').mockResolvedValue({ last_successful_corpus_import_at: null })

    await renderSettingsWithPicker()
    await screen.findByText(RECENCY_NULL_VALUE)
    await selectCorpusFile()
    fireEvent.click(screen.getByRole('button', { name: 'Start import' }))

    expect(await screen.findByText(status === 'cancelled' ? 'Import cancelled' : 'Import failed')).toBeInTheDocument()
    expect(screen.getByText(RECENCY_NULL_VALUE)).toBeInTheDocument()
    expect(mock.getCorpusImportMetadata).toHaveBeenCalledTimes(1)
  })
})
