import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { Overview } from './Overview'
import { RunDetailView } from './RunDetailView'
import { ToastProvider } from '../components/ToastProvider'
import { ApiError } from '../api/core'
import type { Overview as OverviewData } from '../api/overview'
import type { Run } from '../types'

// "Run Now — Resume After a Time Limit" (admin UI design, BR-017 AC7–AC12):
// acknowledgement lines, persistent refusal messages and their links.

const mockGetOverview = vi.fn()
const mockStartRun = vi.fn()
const mockGetRun = vi.fn()
const mockGetRunVideos = vi.fn()

vi.mock('../api/client', () => ({
  getOverview: (...args: unknown[]) => mockGetOverview(...args),
  startRun: (...args: unknown[]) => mockStartRun(...args),
  startDryRun: vi.fn(),
  getRun: (...args: unknown[]) => mockGetRun(...args),
  getRunVideos: (...args: unknown[]) => mockGetRunVideos(...args),
}))

const baseRun: Run = {
  id: 1,
  started_at: '2026-11-09T06:00:00Z',
  run_type: 'manual',
  status: 'completed',
  videos_discovered: 0,
  videos_accepted: 0,
  videos_rejected: 0,
  videos_excluded: 0,
  videos_deferred: 0,
  videos_failed: 0,
  videos_not_yet_evaluated: 0,
  quota_cost: 0,
}

function overviewWith(lastRun: Run | null): OverviewData {
  return {
    last_run: lastRun,
    runs_today: lastRun ? 1 : 0,
    quota: { daily_limit: 10000, consumed_today: 0, remaining: 10000, reset_at: '2026-11-11T00:00:00Z' },
    error_count: 0,
    deferred_count: 0,
  } as OverviewData
}

function StateProbe() {
  const loc = useLocation()
  return <output data-testid="location-state">{JSON.stringify(loc.state)}</output>
}

function renderApp() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/runs" element={<p>Runs list</p>} />
          <Route path="/runs/:id" element={<><RunDetailView /><StateProbe /></>} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  )
}

async function pressRunNow() {
  fireEvent.click(await screen.findByRole('button', { name: /Run Now/i }))
  fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))
}

function refusal(status: number, body: { error?: string; code?: string; runId?: number; reason?: string }) {
  return new ApiError(status, body.error ?? 'refused', { code: body.code, runId: body.runId, reason: body.reason })
}

describe('Overview Run Now acknowledgement and refusals', () => {
  const showModal = HTMLDialogElement.prototype.showModal
  const closeDialog = HTMLDialogElement.prototype.close

  beforeEach(() => {
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
    mockGetOverview.mockResolvedValue(overviewWith(baseRun))
    mockGetRunVideos.mockResolvedValue({ videos: [], total: 0, totalPages: 1 })
    mockGetRun.mockImplementation(async (id: number) => ({ ...baseRun, id, status: 'running', current_phase: 'routing' }))
  })

  afterEach(() => {
    HTMLDialogElement.prototype.showModal = showModal
    HTMLDialogElement.prototype.close = closeDialog
    vi.clearAllMocks()
  })

  it('tells the person in the dialog that Run Now can continue an earlier stopped run', async () => {
    renderApp()
    fireEvent.click(await screen.findByRole('button', { name: /Run Now/i }))
    expect(screen.getByRole('heading', { name: 'Start a processing run now?' })).toBeInTheDocument()
    expect(screen.getByText(/If an earlier Run Now run stopped at its time limit, this continues the oldest such run instead of starting a new one\./)).toBeInTheDocument()
  })

  it.each([
    ['started', 'Run 30 started. Checking progress…'],
    ['resumed', 'Run 30 resumed. It continues from where it stopped. No new run was started.'],
    [undefined, 'Run 30 accepted. Checking progress…'],
    ['something_new', 'Run 30 accepted. Checking progress…'],
  ])('202 admission %s shows its line and focuses the Run Detail heading', async (admission, line) => {
    mockStartRun.mockResolvedValue({ run_id: 30, status: 'running', ...(admission ? { admission } : {}) })
    renderApp()
    await pressRunNow()

    const heading = await screen.findByRole('heading', { level: 1, name: 'Run 30' })
    await waitFor(() => expect(heading).toHaveFocus())
    const status = screen.getByText(line.replace(' Checking progress…', ''), { exact: false, selector: 'strong' })
    expect(status).toBeInTheDocument()
    expect(status.closest('[role="status"]')).toHaveAttribute('aria-live', 'polite')
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows the Checking progress line while the first saved read is pending', async () => {
    mockStartRun.mockResolvedValue({ run_id: 30, status: 'running', admission: 'resumed' })
    mockGetRun.mockReturnValue(new Promise(() => {}))
    renderApp()
    await pressRunNow()

    expect(await screen.findByText(/Run 30 resumed\. It continues from where it stopped\. No new run was started\. Checking progress…/)).toBeInTheDocument()
  })

  it('only a resumed run explains that recorded additions are not repeated', async () => {
    mockStartRun.mockResolvedValue({ run_id: 30, status: 'running', admission: 'resumed' })
    renderApp()
    await pressRunNow()
    expect(await screen.findByText('Playlist additions already recorded as successful are not repeated. An addition whose earlier outcome could not be confirmed stays marked as unconfirmed.')).toBeInTheDocument()
  })

  it('a started run makes no resume claim', async () => {
    mockStartRun.mockResolvedValue({ run_id: 31, status: 'running', admission: 'started' })
    renderApp()
    await pressRunNow()
    await screen.findByRole('heading', { level: 1, name: 'Run 31' })
    expect(screen.queryByText(/Playlist additions already recorded/)).not.toBeInTheDocument()
    expect(screen.queryByText(/resumed/)).not.toBeInTheDocument()
  })

  it('drops the transient resumed line from navigation state so a reload shows ordinary status', async () => {
    mockStartRun.mockResolvedValue({ run_id: 30, status: 'running', admission: 'resumed' })
    renderApp()
    await pressRunNow()
    await screen.findByRole('heading', { level: 1, name: 'Run 30' })
    await waitFor(() => expect(screen.getByTestId('location-state')).toHaveTextContent('null'))
    expect(screen.getByRole('region', { name: 'Run progress' })).toBeInTheDocument()
  })

  describe('production_run_active', () => {
    it('shows a persistent alert, links to View runs, and keeps Run Now enabled', async () => {
      mockStartRun.mockRejectedValue(refusal(409, { code: 'production_run_active', error: 'a production run is already active' }))
      renderApp()
      await pressRunNow()

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('Run Now did nothing. A production run is already running. Nothing was started or resumed.')
      expect(alert).toHaveTextContent('Wait for it to finish, then press Run Now again.')
      expect(within(alert).getByRole('link', { name: 'View runs' })).toHaveAttribute('href', '/runs')
      expect(screen.getByRole('button', { name: /Run Now/i })).toBeEnabled()
      expect(alert).not.toContainElement(document.activeElement as HTMLElement)
      expect(document.activeElement).not.toBe(alert)
      expect(screen.queryByText('Runs list')).not.toBeInTheDocument()
    })

    it('dismisses on request and clears when Run Now is pressed again', async () => {
      mockStartRun.mockRejectedValueOnce(refusal(409, { code: 'production_run_active' }))
      renderApp()
      await pressRunNow()
      await screen.findByRole('alert')
      fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }))
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()

      mockStartRun.mockRejectedValueOnce(refusal(409, { code: 'production_run_active' }))
      await pressRunNow()
      await screen.findByRole('alert')
      let resolveStart: (v: unknown) => void = () => {}
      mockStartRun.mockReturnValueOnce(new Promise((r) => { resolveStart = r }))
      await pressRunNow()
      await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
      resolveStart({ run_id: 40, status: 'running', admission: 'started' })
      await screen.findByRole('heading', { level: 1, name: 'Run 40' })
    })

    it('branches on the machine code and status, never on message text', async () => {
      mockStartRun.mockRejectedValue(refusal(409, { code: 'production_run_active', error: 'run 30 stopped at its time limit but cannot be resumed safely' }))
      renderApp()
      await pressRunNow()
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('A production run is already running.')
      expect(alert).not.toHaveTextContent('cannot be resumed safely')
    })
  })

  describe('resume_unavailable', () => {
    it.each([
      ['snapshot_unavailable', 'Its saved settings are missing.'],
      ['snapshot_unreadable', 'Its saved settings cannot be read.'],
      ['snapshot_inadmissible', 'Its saved settings cannot be used safely.'],
      ['checkpoint_unrecognized', 'Its saved progress point is not recognized.'],
      ['terminal_timestamp_contradiction', 'Its saved record is contradictory: it has a completion time but is marked interrupted.'],
      ['recheck_at_write', 'Its saved state changed while Run Now was checking it. Check the run, then press Run Now again if that is still what you want.'],
      ['brand_new_reason', 'The reason is not available in this version of the interface.'],
    ])('reason %s maps to its sentence and is never shown raw', async (reason, sentence) => {
      mockStartRun.mockRejectedValue(refusal(409, { code: 'resume_unavailable', runId: 30, reason, error: `raw server text ${reason}` }))
      renderApp()
      await pressRunNow()

      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('Run Now did nothing. Run 30 stopped at its time limit but cannot be resumed safely. No new run was started and nothing was changed. ' + sentence)
      expect(alert).not.toHaveTextContent(reason === 'brand_new_reason' ? reason : `raw server text`)
      expect(within(alert).getByRole('link', { name: 'View run 30' })).toHaveAttribute('href', '/runs/30')
      const discardable = reason !== 'recheck_at_write' && reason !== 'brand_new_reason'
      if (discardable) {
        expect(alert).toHaveTextContent('While run 30 is the oldest run stopped this way, Run Now will keep refusing. To unblock Run Now, you can discard run 30. The schedule is not affected.')
        expect(within(alert).getByRole('button', { name: 'Discard run 30' })).toBeInTheDocument()
      } else {
        expect(within(alert).queryByRole('button', { name: /Discard run/ })).not.toBeInTheDocument()
        expect(alert).not.toHaveTextContent('will keep refusing')
        if (reason === 'brand_new_reason') {
          expect(alert).toHaveTextContent('Run Now may keep refusing. Open the run to see whether it can be discarded. The schedule is not affected.')
        }
      }
      expect(alert).toHaveTextContent('Dry Run stays available.')
      expect(screen.getByRole('button', { name: /Run Now/i })).toBeEnabled()
      expect(screen.getByRole('button', { name: /Dry Run/i })).toBeEnabled()
      expect(screen.queryByText('Runs list')).not.toBeInTheDocument()
    })

    it('does not claim a run id the server did not give', async () => {
      mockStartRun.mockRejectedValue(refusal(409, { code: 'resume_unavailable', reason: 'snapshot_unavailable' }))
      renderApp()
      await pressRunNow()
      const alert = await screen.findByRole('alert')
      expect(within(alert).queryByRole('link', { name: /View run/ })).not.toBeInTheDocument()
      expect(alert).toHaveTextContent('Its saved settings are missing.')
    })
  })

  describe('503 and unconfirmed outcomes', () => {
    it('503 says nothing was started or resumed and links to Runs', async () => {
      mockStartRun.mockRejectedValue(refusal(503, { error: 'startup recovery in progress' }))
      renderApp()
      await pressRunNow()
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent("Run Now did nothing. YuHaul can't accept a run right now. It may be starting up or shutting down. Nothing was started or resumed. Try again in a minute.")
      expect(alert).not.toHaveTextContent('startup recovery in progress')
      expect(within(alert).getByRole('link', { name: 'View runs' })).toHaveAttribute('href', '/runs')
      expect(screen.getByRole('button', { name: /Run Now/i })).toBeEnabled()
    })

    it.each([
      ['other server error', () => refusal(500, { error: 'failed to start run' })],
      ['409 without a known code', () => refusal(409, { error: 'conflict' })],
      ['no response', () => new TypeError('Failed to fetch')],
    ])('%s is unconfirmed, never repeats the request, and keeps Run Now off', async (_name, make) => {
      mockStartRun.mockRejectedValue(make())
      renderApp()
      await pressRunNow()
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('Could not confirm whether a run started or resumed. Check Runs before pressing Run Now again.')
      expect(alert).not.toHaveTextContent('Run Now did nothing')
      expect(within(alert).getByRole('link', { name: 'View runs' })).toHaveAttribute('href', '/runs')
      expect(screen.getByRole('button', { name: /Run Now/i })).toBeDisabled()
      expect(mockStartRun).toHaveBeenCalledTimes(1)
    })
  })
})
