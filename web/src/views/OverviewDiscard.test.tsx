import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Overview } from './Overview'
import { ToastProvider } from '../components/ToastProvider'
import { ApiError } from '../api/core'
import type { Overview as OverviewData } from '../api/overview'
import type { Run } from '../types'

const mockGetOverview = vi.fn()
const mockStartRun = vi.fn()
const mockDiscardRun = vi.fn()

vi.mock('../api/client', () => ({
  getOverview: (...a: unknown[]) => mockGetOverview(...a),
  startRun: (...a: unknown[]) => mockStartRun(...a),
  startDryRun: vi.fn(),
}))
vi.mock('../api/runs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/runs')>()),
  discardRun: (...a: unknown[]) => mockDiscardRun(...a),
}))

const lastRun: Run = {
  id: 30, started_at: '2026-11-09T06:00:00Z', run_type: 'manual', status: 'interrupted',
  videos_discovered: 0, videos_accepted: 0, videos_rejected: 0, videos_excluded: 0, videos_deferred: 0,
  videos_failed: 0, videos_not_yet_evaluated: 0, quota_cost: 0,
}
const overview = (run: Run): OverviewData => ({
  last_run: run, runs_today: 1, error_count: 0, deferred_count: 0,
  quota: { daily_limit: 10000, consumed_today: 0, remaining: 10000, reset_at: '2026-11-11T00:00:00Z' },
}) as OverviewData

async function refuse(reason: string) {
  mockStartRun.mockRejectedValue(new ApiError(409, 'refused', { code: 'resume_unavailable', runId: 30, reason }))
  render(<ToastProvider><MemoryRouter><Overview /></MemoryRouter></ToastProvider>)
  fireEvent.click(await screen.findByRole('button', { name: /Run Now/i }))
  fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  return screen.findByRole('alert')
}

describe('Overview — discard from the Run Now refusal', () => {
  beforeEach(() => {
    // jsdom has no <dialog> modal support; the Run Now confirmation needs it.
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
    mockGetOverview.mockReset().mockResolvedValue(overview(lastRun))
    mockStartRun.mockReset(); mockDiscardRun.mockReset()
  })

  it.each([
    'snapshot_unavailable', 'snapshot_unreadable', 'snapshot_inadmissible',
    'checkpoint_unrecognized', 'terminal_timestamp_contradiction',
  ])('shows Discard run 30 for %s', async (reason) => {
    const alert = await refuse(reason)
    expect(within(alert).getByRole('button', { name: 'Discard run 30' })).toBeInTheDocument()
  })

  it.each(['recheck_at_write', 'brand_new_reason'])('shows no button for %s', async (reason) => {
    const alert = await refuse(reason)
    expect(within(alert).queryByRole('button', { name: /Discard run/ })).toBeNull()
  })

  it('re-reads the overview after a discard and replaces the refusal promise with the acknowledgement', async () => {
    mockDiscardRun.mockResolvedValue({ run_id: 30, status: 'failed', outcome: 'discarded', cause: 'snapshot_unavailable' })
    const alert = await refuse('snapshot_unavailable')
    expect(mockGetOverview).toHaveBeenCalledTimes(1)
    fireEvent.click(within(alert).getByRole('button', { name: 'Discard run 30' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Discard run 30' }))

    expect(await screen.findByText(/run 30 is now marked failed and will not be resumed/)).toBeInTheDocument()
    await waitFor(() => expect(mockGetOverview).toHaveBeenCalledTimes(2))
    expect(screen.queryByText(/will keep refusing/)).toBeNull()
  })

  it('re-reads the overview after a refusal', async () => {
    mockDiscardRun.mockRejectedValue(new ApiError(409, 'x', { code: 'run_not_discardable' }))
    const alert = await refuse('snapshot_unavailable')
    fireEvent.click(within(alert).getByRole('button', { name: 'Discard run 30' }))
    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Discard run 30' }))
    await waitFor(() => expect(mockGetOverview).toHaveBeenCalledTimes(2))
    expect(await screen.findByText(/Discard did nothing\. Run 30 cannot be discarded now/)).toBeInTheDocument()
  })
})
