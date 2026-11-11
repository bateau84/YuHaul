import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, within, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { RunDetailView } from './RunDetailView'
import { ToastProvider } from '../components/ToastProvider'
import type { Run } from '../types'

const mockGetRun = vi.fn()
const mockGetRunVideos = vi.fn()
const mockEndRun = vi.fn()

vi.mock('../api/client', () => ({
  getRun: (...a: unknown[]) => mockGetRun(...a),
  getRunVideos: (...a: unknown[]) => mockGetRunVideos(...a),
}))
vi.mock('../api/runs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/runs')>()),
  endRun: (...a: unknown[]) => mockEndRun(...a),
}))

const END = 'diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user'
const DISCARD = 'diag:v1 phase=unknown operation=discard_run code=snapshot_unavailable'

// Run 14: saved as running by a version-0 server; nothing executes it.
const run14: Run = {
  id: 14, started_at: '2026-10-20T06:00:00Z', run_type: 'manual', status: 'running',
  videos_discovered: 19615, videos_accepted: 0, videos_rejected: 0, videos_excluded: 0, videos_deferred: 0,
  videos_failed: 0, videos_not_yet_evaluated: 19615, quota_cost: 0, current_phase: 'discovery', initiated_by: null,
  interrupt_reason: null, endable: true, executing: false, discardable: false,
}

function renderPage() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/runs/14']}>
        <Routes><Route path="/runs/:id" element={<RunDetailView />} /></Routes>
      </MemoryRouter>
    </ToastProvider>,
  )
}

describe('Run Detail — mark a legacy run as ended', () => {
  beforeEach(() => {
    mockGetRun.mockReset(); mockEndRun.mockReset()
    mockGetRunVideos.mockReset().mockResolvedValue({ videos: [], total: 0, totalPages: 1 })
  })

  it('replaces the running text for an endable running run: no progress claim, no Stop, no Run Now promise', async () => {
    mockGetRun.mockResolvedValue(run14)
    renderPage()
    const region = await screen.findByRole('region', { name: 'Mark run as ended' })
    expect(region).toHaveTextContent('Marked as running — nothing is executing it.')
    expect(region).toHaveTextContent('Run 14 was created by an older version of YuHaul and cannot be resumed. It will not end by itself. Last recorded phase: Discovering videos. Work saved so far is kept.')
    expect(region).toHaveTextContent('You can mark it as ended. That marks it failed and leaves its unfinished videos unprocessed.')
    expect(within(region).getByRole('button', { name: 'Mark run 14 as ended' })).toBeInTheDocument()
    expect(screen.queryByText(/Run in progress/)).toBeNull()
    expect(screen.queryByRole('region', { name: 'Run progress' })).toBeNull()
    expect(screen.queryByRole('button', { name: /^Stop run/ })).toBeNull()
    expect(screen.queryByText(/Run Now continues/)).toBeNull()
  })

  it('shows "Run interrupted." for an endable interrupted run', async () => {
    mockGetRun.mockResolvedValue({ ...run14, status: 'interrupted' })
    renderPage()
    const region = await screen.findByRole('region', { name: 'Mark run as ended' })
    expect(region).toHaveTextContent('Run interrupted.')
    expect(screen.queryByText(/Run interrupted — time limit reached/)).toBeNull()
    expect(screen.queryByText(/No interruption reason was recorded/)).toBeNull()
    expect(within(region).getByRole('button', { name: 'Mark run 14 as ended' })).toBeInTheDocument()
  })

  it.each([
    ['endable false', { endable: false }],
    ['endable null', { endable: null }],
    ['endable absent (older server)', { endable: undefined }],
  ])('offers no control and no end text when %s', async (_name, patch) => {
    mockGetRun.mockResolvedValue({ ...run14, current_phase: 'routing', ...patch })
    renderPage()
    await screen.findByRole('region', { name: 'Run progress' })
    expect(screen.queryByRole('region', { name: 'Mark run as ended' })).toBeNull()
    expect(screen.queryByRole('button', { name: /Mark run/ })).toBeNull()
  })

  // Run 14 while another production run is active: endable is false for every
  // run then, and executing is false for run 14. It must not get Stop.
  it('never shows Stop for run 14 while another production run is active', async () => {
    mockGetRun.mockResolvedValue({ ...run14, endable: false, executing: false })
    renderPage()
    await screen.findByRole('region', { name: 'Run progress' })
    expect(screen.queryByRole('button', { name: /^Stop run/ })).toBeNull()
    expect(screen.queryByRole('button', { name: /Mark run/ })).toBeNull()
  })

  it('still offers Stop for a run the server is executing', async () => {
    mockGetRun.mockResolvedValue({ ...run14, id: 14, executing: true, endable: false, initiated_by: 'schedule', run_type: 'scheduled' })
    renderPage()
    expect(await screen.findByRole('button', { name: 'Stop run 14' })).toBeInTheDocument()
  })

  it('sends nothing on cancel; confirm sends one request, shows the acknowledgement and then the ended display', async () => {
    mockGetRun.mockResolvedValueOnce(run14)
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Mark run 14 as ended' }))
    fireEvent.click(screen.getByRole('button', { name: 'Keep run 14 as it is' }))
    expect(mockEndRun).not.toHaveBeenCalled()

    mockEndRun.mockResolvedValue({ run_id: 14, status: 'failed', outcome: 'ended' })
    mockGetRun.mockResolvedValue({ ...run14, status: 'failed', interrupt_reason: END, endable: false })
    fireEvent.click(screen.getByRole('button', { name: 'Mark run 14 as ended' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Mark run 14 as ended' }))

    const ack = await screen.findByText(/Marked as ended:/)
    expect(ack.closest('[role="status"]')).toHaveFocus()
    expect(mockEndRun).toHaveBeenCalledTimes(1)
    expect(mockEndRun).toHaveBeenCalledWith(14)
    await waitFor(() => expect(screen.getByText('Run failed — marked as ended by user (legacy run).')).toBeInTheDocument())
    expect(screen.getByText(/You marked this run as ended\. It did not fail by itself\./)).toBeInTheDocument()
    expect(screen.queryByText(/Reason: diag:v1/)).toBeNull()
    expect(screen.queryByText(/deferred queue/)).toBeNull()
    expect(screen.queryByRole('button', { name: /Mark run|Stop run|Discard run/ })).toBeNull()
    expect(screen.getByText(/Marked as ended:/)).toBeInTheDocument()
  })

  it('re-reads the run after a refusal and withdraws the button when the run is no longer endable', async () => {
    mockGetRun.mockResolvedValueOnce(run14)
    mockGetRun.mockResolvedValue({ ...run14, status: 'failed', interrupt_reason: END, endable: false })
    const { ApiError } = await import('../api/core')
    mockEndRun.mockRejectedValue(new ApiError(409, 'raw', { code: 'run_ended', runId: 14, savedStatus: 'failed' }))
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Mark run 14 as ended' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Mark run 14 as ended' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Mark as ended did nothing. Run 14 has already ended.')
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Mark run 14 as ended' })).toBeNull())
    expect(mockGetRun.mock.calls.length).toBeGreaterThan(1)
  })

  it('tells four different endings apart: ended, discarded, stopped and self-failed', async () => {
    const failed = { ...run14, status: 'failed' as const, endable: false, completed_at: '2026-10-21T06:00:00Z' }
    mockGetRun.mockResolvedValueOnce({ ...failed, interrupt_reason: END })
    const ended = renderPage()
    expect(await screen.findByText('Run failed — marked as ended by user (legacy run).')).toBeInTheDocument()
    ended.unmount()

    mockGetRun.mockResolvedValueOnce({ ...failed, interrupt_reason: DISCARD })
    const discarded = renderPage()
    expect(await screen.findByText('Run failed — discarded by user.')).toBeInTheDocument()
    expect(screen.getByText(/Reason: diag:v1 phase=unknown operation=discard_run/)).toBeInTheDocument()
    expect(screen.getByText(/Its unfinished videos are in the deferred queue/)).toBeInTheDocument()
    expect(screen.queryByText(/marked as ended/)).toBeNull()
    discarded.unmount()

    mockGetRun.mockResolvedValueOnce({ ...run14, status: 'stopped', endable: false, stop_requested_at: '2026-10-21T06:00:00Z', interrupt_reason: 'diag:v1 phase=unknown operation=stop_run code=stopped_by_user' })
    const stopped = renderPage()
    expect(await screen.findByText('Run stopped by user')).toBeInTheDocument()
    stopped.unmount()

    mockGetRun.mockResolvedValueOnce({ ...failed, interrupt_reason: 'diag:v1 phase=discovery operation=discover code=timeout' })
    renderPage()
    expect(await screen.findByText('Run failed')).toBeInTheDocument()
    expect(screen.queryByText(/by user/)).toBeNull()
    expect(screen.getByText(/Reason: diag:v1 phase=discovery/)).toBeInTheDocument()
  })

  it('makes no claim about who ended a failed run whose machine reason is absent', async () => {
    mockGetRun.mockResolvedValue({ ...run14, status: 'failed', endable: false, interrupt_reason: null })
    renderPage()
    expect(await screen.findByText('Run failed')).toBeInTheDocument()
    expect(screen.queryByText(/by user/)).toBeNull()
    expect(screen.getByText('No failure reason was recorded for this run.')).toBeInTheDocument()
  })
})
