import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { RunDetailView } from './RunDetailView'
import { ToastProvider } from '../components/ToastProvider'
import { ApiError } from '../api/core'
import type { Run } from '../types'

const mockGetRun = vi.fn()
const mockGetRunVideos = vi.fn()
const mockDiscardRun = vi.fn()

vi.mock('../api/client', () => ({
  getRun: (...a: unknown[]) => mockGetRun(...a),
  getRunVideos: (...a: unknown[]) => mockGetRunVideos(...a),
}))
vi.mock('../api/runs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/runs')>()),
  discardRun: (...a: unknown[]) => mockDiscardRun(...a),
}))

const interrupted: Run = {
  id: 30, started_at: '2026-11-10T06:00:00Z', run_type: 'manual', status: 'interrupted',
  videos_discovered: 3, videos_accepted: 0, videos_rejected: 0, videos_excluded: 0, videos_deferred: 0,
  videos_failed: 0, videos_not_yet_evaluated: 0, quota_cost: 0, initiated_by: 'run_now', interrupt_cause: 'time_limit',
}

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/runs/30']}>
        <Routes><Route path="/runs/:id" element={<RunDetailView />} /></Routes>
      </MemoryRouter>
    </ToastProvider>,
  )
}

describe('Run Detail — discard an unusable time-limit run', () => {
  beforeEach(() => {
    mockGetRun.mockReset(); mockDiscardRun.mockReset()
    mockGetRunVideos.mockReset().mockResolvedValue({ videos: [], total: 0, totalPages: 1 })
  })

  it.each([
    ['snapshot_unavailable', 'Its saved settings are missing.'],
    ['snapshot_unreadable', 'Its saved settings cannot be read.'],
    ['snapshot_inadmissible', 'Its saved settings cannot be used safely.'],
    ['checkpoint_unrecognized', 'Its saved progress point is not recognized.'],
    ['terminal_timestamp_contradiction', 'Its saved record is contradictory: it has a completion time but is marked interrupted.'],
    [undefined, 'Its saved data cannot be used to continue it safely.'],
  ])('offers the control with the %s sentence when the read says discardable', async (cause, sentence) => {
    mockGetRun.mockResolvedValue({ ...interrupted, discardable: true, discard_cause: cause })
    renderPage()
    const region = await screen.findByRole('region', { name: 'Discard run' })
    expect(region).toHaveTextContent(`Run 30 cannot be resumed. ${sentence} Run Now will keep refusing until run 30 is discarded.`)
    expect(within(region).getByRole('button', { name: 'Discard run 30' })).toBeInTheDocument()
    expect(screen.queryByText(/This run continues only if it is the oldest/)).toBeNull()
  })

  it.each([
    ['discardable false', { discardable: false }],
    ['discardable absent', {}],
  ])('shows no control and no refusal promise when %s', async (_n, extra) => {
    mockGetRun.mockResolvedValue({ ...interrupted, ...extra })
    renderPage()
    await screen.findByRole('heading', { name: 'Run 30' })
    expect(screen.queryByRole('button', { name: /Discard run/ })).toBeNull()
    expect(screen.queryByText(/will keep refusing/)).toBeNull()
    expect(screen.getByText(/This run continues only if it is the oldest/)).toBeInTheDocument()
  })

  it('re-reads the run after a discard and keeps the acknowledgement while status turns failed', async () => {
    mockGetRun.mockResolvedValueOnce({ ...interrupted, discardable: true, discard_cause: 'snapshot_unavailable' })
    mockGetRun.mockResolvedValue({ ...interrupted, status: 'failed', completed_at: '2026-11-10T07:00:00Z', interrupt_reason: 'discarded by user' })
    mockDiscardRun.mockResolvedValue({ run_id: 30, status: 'failed', outcome: 'discarded', cause: 'snapshot_unavailable' })
    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Discard run 30' }))
    const dialog = screen.getByRole('dialog')
    fireEvent.click(within(dialog).getByRole('button', { name: 'Discard run 30' }))

    expect(await screen.findByText(/run 30 is now marked failed and will not be resumed/)).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Run failed')).toBeInTheDocument())
    expect(mockGetRun).toHaveBeenCalledTimes(2)
    expect(screen.getByRole('link', { name: 'View deferred queue' })).toHaveAttribute('href', '/errors?tab=deferred')
    expect(screen.queryByRole('button', { name: /Discard run/ })).toBeNull()
  })

  it('re-reads the run after a refusal, keeps the message and withdraws the button when no longer discardable', async () => {
    mockGetRun.mockResolvedValueOnce({ ...interrupted, discardable: true, discard_cause: 'snapshot_unavailable' })
    mockGetRun.mockResolvedValue({ ...interrupted, status: 'failed' })
    mockDiscardRun.mockRejectedValue(new ApiError(409, 'ended', { code: 'run_ended' }))
    renderPage()

    fireEvent.click(await screen.findByRole('button', { name: 'Discard run 30' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Discard run 30' }))

    expect(await screen.findByText(/Discard did nothing\. Run 30 has already ended/)).toBeInTheDocument()
    await waitFor(() => expect(mockGetRun).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Discard run 30' })).toBeNull())
    expect(screen.getByText(/Discard did nothing\. Run 30 has already ended/)).toBeInTheDocument()
  })
})
