import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RunDetailView } from './RunDetailView'
import { ToastProvider } from '../components/ToastProvider'
import type { Run } from '../types'

// Run Detail copy for a manual Run Now run stopped at its time limit
// ("Run Now — Resume After a Time Limit", design 2026-11-10).

const mockGetRun = vi.fn()
const mockGetRunVideos = vi.fn()

vi.mock('../api/client', () => ({
  getRun: (...args: unknown[]) => mockGetRun(...args),
  getRunVideos: (...args: unknown[]) => mockGetRunVideos(...args),
}))

const interrupted: Run = {
  id: 30,
  started_at: '2026-11-09T06:00:00Z',
  run_type: 'manual',
  status: 'interrupted',
  videos_discovered: 4,
  videos_accepted: 1,
  videos_rejected: 0,
  videos_excluded: 0,
  videos_deferred: 0,
  videos_failed: 0,
  videos_not_yet_evaluated: 3,
  quota_cost: 0,
  current_phase: 'action_execution',
  initiated_by: 'run_now',
  interrupt_cause: 'time_limit',
}

async function renderRun(run: Run) {
  mockGetRun.mockResolvedValue(run)
  mockGetRunVideos.mockResolvedValue({ videos: [], total: 0, totalPages: 1 })
  render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/runs/30']}>
        <Routes>
          <Route path="/" element={<p>Overview page</p>} />
          <Route path="/runs/:id" element={<RunDetailView />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  )
  await screen.findByRole('heading', { level: 1 })
}

const timeLimitExplanation = 'Work done up to this point is saved. Pressing Run Now continues the oldest run stopped this way. This run continues only if it is the oldest and can be resumed safely.'

describe('Run Detail — manual run stopped at its time limit', () => {
  afterEach(() => vi.clearAllMocks())

  it('says the time limit was reached, keeps phase and counts, and links to Overview', async () => {
    await renderRun(interrupted)

    expect(screen.getByText('Run interrupted — time limit reached.')).toBeInTheDocument()
    expect(screen.getByText(/Work done up to this point is saved\./).textContent).toContain(timeLimitExplanation)
    expect(screen.getByRole('link', { name: 'Go to Overview' })).toHaveAttribute('href', '/')
    expect(screen.getByText('Last recorded phase: Processing playlist actions')).toBeInTheDocument()
    expect(screen.getByText('Discovered').previousSibling).toHaveTextContent('4')
  })

  it('offers no Resume or Cancel button and makes no promise to resume', async () => {
    await renderRun(interrupted)

    expect(screen.queryByRole('button', { name: /resume|cancel/i })).not.toBeInTheDocument()
    expect(document.body.textContent).not.toMatch(/will resume|resumes next|resumes automatically/i)
  })

  it.each([
    ['shutdown', { interrupt_cause: 'shutdown' as const }],
    ['unknown cause (null)', { interrupt_cause: null }],
    ['cause not reported by the API', { interrupt_cause: undefined }],
    ['scheduled run', { run_type: 'scheduled' as const, initiated_by: 'schedule' as const }],
    ['legacy run without initiator', { initiated_by: null }],
    ['initiator not reported by the API', { initiated_by: undefined }],
    ['queue retry run', { initiated_by: 'queue_retry' as const }],
    ['failed run', { status: 'failed' as const }],
    ['dry run', { run_type: 'dry_run' as const }],
    ['backtest', { run_type: 'backtest' as const }],
  ])('uses the generic copy for %s', async (_name, patch) => {
    await renderRun({ ...interrupted, ...patch })

    expect(screen.queryByText('Run interrupted — time limit reached.')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(timeLimitExplanation)
    expect(screen.queryByRole('link', { name: 'Go to Overview' })).not.toBeInTheDocument()
    expect(screen.getByText(patch.status === 'failed' ? 'Run failed' : 'Run interrupted')).toBeInTheDocument()
  })
})
