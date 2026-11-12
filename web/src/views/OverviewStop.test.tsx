import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Overview } from './Overview'
import { ToastProvider } from '../components/ToastProvider'
import type { Overview as OverviewData } from '../api/overview'
import type { Run } from '../types'

const mockGetOverview = vi.fn()
const mockStopRun = vi.fn()

vi.mock('../api/client', () => ({
  getOverview: (...a: unknown[]) => mockGetOverview(...a),
  startRun: vi.fn(),
  startDryRun: vi.fn(),
}))
vi.mock('../api/runs', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../api/runs')>()),
  stopRun: (...a: unknown[]) => mockStopRun(...a),
}))

const run: Run = {
  id: 41, started_at: '2026-11-10T06:00:00Z', run_type: 'scheduled', status: 'running',
  videos_discovered: 0, videos_accepted: 0, videos_rejected: 0, videos_excluded: 0, videos_deferred: 0,
  videos_failed: 0, videos_not_yet_evaluated: 0, quota_cost: 0, current_phase: 'routing', initiated_by: 'schedule', executing: true,
}
const ov = (r: Run | null): OverviewData => ({
  last_run: r, runs_today: r ? 1 : 0, error_count: 0, deferred_count: 0,
  quota: { daily_limit: 10000, consumed_today: 0, remaining: 10000, reset_at: '2026-11-11T00:00:00Z' },
}) as OverviewData

function renderPage() {
  return render(<ToastProvider><MemoryRouter><Overview /></MemoryRouter></ToastProvider>)
}

describe('Overview — stop an active run', () => {
  beforeEach(() => { mockGetOverview.mockReset(); mockStopRun.mockReset() })

  it('offers Stop in the latest-run panel for an active run and not for a finished one', async () => {
    mockGetOverview.mockResolvedValue(ov(run))
    const { unmount } = renderPage()
    expect(await screen.findByRole('button', { name: 'Stop run 41' })).toBeTruthy()
    unmount()
    mockGetOverview.mockResolvedValue(ov({ ...run, status: 'completed' }))
    renderPage()
    await screen.findByText(/Run 41 — scheduled — Completed/)
    expect(screen.queryByRole('button', { name: /Stop run/ })).toBeNull()
  })

  it('acknowledges, then shows Stopping with Run Now unavailable', async () => {
    mockGetOverview.mockResolvedValueOnce(ov(run)).mockResolvedValue(ov({ ...run, stop_requested_at: '2026-11-10T07:00:00Z' }))
    mockStopRun.mockResolvedValue({ run_id: 41, status: 'running', stop: 'accepted' })
    renderPage()
    fireEvent.click(await screen.findByRole('button', { name: 'Stop run 41' }))
    fireEvent.click(screen.getAllByRole('button', { name: 'Stop run 41' }).find((b) => b.closest('[role="dialog"]'))!)
    const ack = await screen.findByText(/Stopping: run 41 is stopping and will never be resumed/)
    await waitFor(() => expect(document.activeElement).toBe(ack.closest('[role="status"]')))
    expect(await screen.findByText(/Run 41 is stopping\. Run Now stays unavailable/)).toBeTruthy()
    expect(screen.getByText(/Run 41 — Stopping/)).toBeTruthy()
    expect((screen.getByRole('button', { name: /Run Now/ }) as HTMLButtonElement).disabled).toBe(true)
    expect(screen.getAllByRole('link', { name: /View run/ }).length).toBeGreaterThan(0)
    expect(screen.queryByRole('button', { name: 'Stop run 41' })).toBeNull()
  })

  it('shows a stopped run as Stopped by user with Run Now available', async () => {
    mockGetOverview.mockResolvedValue(ov({ ...run, status: 'stopped' }))
    renderPage()
    expect(await screen.findByText(/Run 41 — scheduled — Stopped by user/)).toBeTruthy()
    expect((screen.getByRole('button', { name: /Run Now/ }) as HTMLButtonElement).disabled).toBe(false)
  })
})
