import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { RunDetailView, RUN_POLL_INTERVAL_MS } from './RunDetailView'
import { ToastProvider } from '../components/ToastProvider'
import type { Run } from '../types'

const mockGetRun = vi.fn()
const mockGetRunVideos = vi.fn()
const mockStopRun = vi.fn()

vi.mock('../api/client', () => ({
  getRun: (...a: unknown[]) => mockGetRun(...a),
  getRunVideos: (...a: unknown[]) => mockGetRunVideos(...a),
}))
vi.mock('../api/runs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/runs')>()),
  stopRun: (...a: unknown[]) => mockStopRun(...a),
}))

const base: Run = {
  id: 41, started_at: '2026-11-10T06:00:00Z', run_type: 'scheduled', status: 'running',
  videos_discovered: 3, videos_accepted: 0, videos_rejected: 0, videos_excluded: 0, videos_deferred: 0,
  videos_failed: 0, videos_not_yet_evaluated: 0, quota_cost: 0, current_phase: 'action_execution', initiated_by: 'schedule',
}

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/runs/41']}>
        <Routes><Route path="/runs/:id" element={<RunDetailView />} /></Routes>
      </MemoryRouter>
    </ToastProvider>,
  )
}

describe('Run Detail — stop an active run', () => {
  beforeEach(() => {
    mockGetRun.mockReset(); mockStopRun.mockReset()
    mockGetRunVideos.mockReset().mockResolvedValue({ videos: [], total: 0, totalPages: 1 })
  })
  afterEach(() => vi.useRealTimers())

  it('offers Stop in the progress region while running', async () => {
    mockGetRun.mockResolvedValue(base)
    renderPage()
    expect(await screen.findByRole('button', { name: 'Stop run 41' })).toBeTruthy()
  })

  it('does not offer Stop for interrupted, completed, failed or stopped runs', async () => {
    for (const status of ['interrupted', 'completed', 'failed', 'stopped'] as const) {
      mockGetRun.mockResolvedValue({ ...base, status })
      const { unmount } = renderPage()
      await screen.findByRole('heading', { name: 'Run 41' })
      expect(screen.queryByRole('button', { name: /Stop run/ })).toBeNull()
      unmount()
    }
  })

  it('acknowledges, moves focus, then shows Stopping and no Running claim', async () => {
    mockGetRun.mockResolvedValueOnce(base).mockResolvedValue({ ...base, stop_requested_at: '2026-11-10T07:00:00Z' })
    mockStopRun.mockResolvedValue({ run_id: 41, status: 'running', stop: 'accepted' })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Stop run 41' }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Stop run 41' }).find((b) => b.closest('[role="dialog"]'))!)
    const ack = await screen.findByText(/Stopping: run 41 is stopping/)
    await waitFor(() => expect(document.activeElement).toBe(ack.closest('[role="status"]')))
    await screen.findByText('Stopping — Processing playlist actions. Run 41 has been told to stop and is saving its work.')
    expect(screen.queryByText(/Run in progress/)).toBeNull()
    expect(screen.queryByRole('button', { name: /Stop run 41/ })).toBeNull()
    expect(screen.getByText('Stopping run 41…')).toBeTruthy()
    expect(screen.queryByText('Link')).toBeNull()
  })

  it('shows a refusal alert without moving focus and re-reads the run', async () => {
    const { ApiError } = await import('../api/core')
    mockGetRun.mockResolvedValueOnce(base).mockResolvedValue({ ...base, status: 'completed' })
    mockStopRun.mockRejectedValue(new ApiError(409, 'x', { code: 'run_ended', runId: 41 }))
    renderPage()
    const trigger = await screen.findByRole('button', { name: 'Stop run 41' })
    fireEvent.click(trigger)
    fireEvent.click(screen.getAllByRole('button', { name: 'Stop run 41' }).find((b) => b.closest('[role="dialog"]'))!)
    const alert = await screen.findByText(/Run 41 has already ended/)
    expect(alert.closest('[role="alert"]')).toBeTruthy()
    expect(document.activeElement).not.toBe(alert.closest('[role="alert"]'))
    await waitFor(() => expect(screen.queryByRole('button', { name: /Stop run/ })).toBeNull())
  })

  it('keeps Stopping as the last known status when the status read fails', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mockGetRun.mockResolvedValueOnce({ ...base, stop_requested_at: '2026-11-10T07:00:00Z' }).mockRejectedValue(new Error('down'))
    renderPage()
    await screen.findByText(/Stopping — Processing playlist actions/)
    await act(async () => { await vi.advanceTimersByTimeAsync(RUN_POLL_INTERVAL_MS + 10) })
    expect((await screen.findByText(/Could not refresh status/)).textContent).toContain('Last known status: Stopping.')
  })

  it('shows the production stopped state with the deferred-queue link, not a failure', async () => {
    mockGetRun.mockResolvedValue({ ...base, status: 'stopped', completed_at: '2026-11-10T07:05:00Z' })
    renderPage()
    expect(await screen.findByText('Run stopped by user')).toBeTruthy()
    expect(screen.getByText(/Last recorded phase: Processing playlist actions\./)).toBeTruthy()
    expect(screen.getByText(/Run 41 will not be resumed\. Its unfinished videos are in the deferred queue/)).toBeTruthy()
    expect(screen.getByRole('link', { name: 'View deferred queue' }).getAttribute('href')).toBe('/errors')
    expect(screen.queryByText(/Run failed/)).toBeNull()
  })

  it('shows the simulation stopped state without a deferred sentence and with the no-results copy', async () => {
    mockGetRun.mockResolvedValue({ ...base, run_type: 'dry_run', initiated_by: null, status: 'stopped', videos_selected: 0, videos_evaluated: 0 })
    renderPage()
    expect(await screen.findByText(/Results are incomplete because the run was stopped\./)).toBeTruthy()
    expect(screen.queryByText(/deferred queue/)).toBeNull()
    expect(await screen.findByText('No results were recorded before the run was stopped.')).toBeTruthy()
    expect(screen.queryByText(/No videos were selected for this dry run/)).toBeNull()
  })

  it('moves from Stopping to Stopped by user on poll and stops polling', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    mockGetRun
      .mockResolvedValueOnce({ ...base, stop_requested_at: '2026-11-10T07:00:00Z' })
      .mockResolvedValue({ ...base, status: 'stopped', stop_requested_at: '2026-11-10T07:00:00Z' })
    renderPage()
    await screen.findByText(/Stopping — /)
    await act(async () => { await vi.advanceTimersByTimeAsync(RUN_POLL_INTERVAL_MS + 10) })
    expect(await screen.findByText('Run stopped by user')).toBeTruthy()
    const calls = mockGetRun.mock.calls.length
    await act(async () => { await vi.advanceTimersByTimeAsync(RUN_POLL_INTERVAL_MS * 2) })
    expect(mockGetRun.mock.calls.length).toBe(calls)
  })
})
