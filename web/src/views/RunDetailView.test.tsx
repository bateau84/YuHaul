import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RunDetailView } from './RunDetailView'
import { ToastProvider } from '../components/ToastProvider'
import type { Run, VideoFinalOutcome, VideoOutcome } from '../types'

const mockGetRun = vi.fn()
const mockGetRunVideos = vi.fn()
const mockSubscribeRunSSE = vi.fn()

vi.mock('../api/client', () => ({
  getRun: (...args: unknown[]) => mockGetRun(...args),
  getRunVideos: (...args: unknown[]) => mockGetRunVideos(...args),
  subscribeRunSSE: (...args: unknown[]) => mockSubscribeRunSSE(...args),
}))

const runFixture: Run = {
  id: 7,
  started_at: '2026-09-08T06:00:00Z',
  run_type: 'manual',
  status: 'completed',
  videos_discovered: 6,
  videos_accepted: 1,
  videos_rejected: 1,
  videos_excluded: 1,
  videos_deferred: 1,
  videos_failed: 1,
  videos_not_yet_evaluated: 0,
  quota_cost: 10,
}

function videoFixture(
  id: number,
  videoId: string,
  processingState: VideoOutcome['processing_state'],
  finalOutcome: VideoOutcome['final_outcome'],
): VideoOutcome {
  return {
    id,
    video_id: videoId,
    channel_id: 'c1',
    title: `Title ${videoId}`,
    channel_name: 'Channel One',
    processing_state: processingState,
    final_outcome: finalOutcome,
  }
}

// One video per BR-032 outcome category. Excluded shares processing_state
// "evaluated" with rejected, so category identity lives in final_outcome.
const allVideos: VideoOutcome[] = [
  videoFixture(1, 'v-accepted', 'completed', 'accepted'),
  videoFixture(2, 'v-rejected', 'evaluated', 'rejected'),
  videoFixture(3, 'v-excluded', 'evaluated', 'excluded'),
  videoFixture(4, 'v-deferred', 'deferred', 'deferred'),
  videoFixture(5, 'v-failed', 'failed', 'failed'),
  videoFixture(6, 'v-pending', 'not_yet_evaluated', 'not_yet_evaluated'),
]

// Server-side filter stand-in: mirrors GET /api/runs/:id/videos?outcome=,
// including the filtered total the empty state depends on.
function serveVideos(videos: VideoOutcome[]) {
  mockGetRunVideos.mockImplementation((_id: number, query?: { outcome?: VideoFinalOutcome }) => {
    const filtered = query?.outcome ? videos.filter((v) => v.final_outcome === query.outcome) : videos
    return Promise.resolve({ videos: filtered, total: filtered.length })
  })
}

function renderView() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/runs/7']}>
        <Routes>
          <Route path="/runs/:id" element={<RunDetailView />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  )
}

describe('RunDetailView outcome filter (BR-032 AC5)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetRun.mockResolvedValue(runFixture)
    mockSubscribeRunSSE.mockReturnValue(() => {})
    serveVideos(allVideos)
  })

  it('offers every BR-032 outcome category in the filter', async () => {
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    const select = screen.getByLabelText('Outcome')
    const options = within(select).getAllByRole('option').map((o) => o.textContent)
    expect(options).toEqual([
      'All outcomes',
      'Accepted',
      'Rejected',
      'Excluded',
      'Deferred',
      'Failed',
      'Not evaluated',
    ])
  })

  it.each(['backtest', 'replay', 'dry_run'] as const)('shows hypothetical requested-run results for %s', async (run_type) => {
    mockGetRun.mockResolvedValue({ ...runFixture, run_type })
    serveVideos([
      videoFixture(1, 'simulation-accepted', 'evaluated', 'accepted'),
      videoFixture(2, 'simulation-rejected', 'completed', 'rejected'),
    ])
    renderView()
    await screen.findByText('Hypothetical accepted')
    const title = run_type === 'dry_run' ? 'Dry-run 7' : `${run_type === 'backtest' ? 'Backtest' : 'Replay'} run 7`
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: title })).toHaveFocus())
    expect(screen.getByText('Hypothetical rejected')).toBeInTheDocument()
    expect(screen.getByText('Simulation only. Results are hypothetical; no playlist actions were executed by this run.')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Hypothetical results' })).toBeInTheDocument()
    expect(screen.getByText('Evaluated videos')).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('Hypothetical outcome'), { target: { value: 'rejected' } })
    await waitFor(() => expect(screen.queryByText('Hypothetical accepted')).not.toBeInTheDocument())
    expect(screen.getByText('Hypothetical rejected')).toBeInTheDocument()
  })

  it('explains deferred and failed simulation evaluations rather than production lifecycle', async () => {
    mockGetRun.mockResolvedValue({ ...runFixture, run_type: 'backtest' })
    serveVideos([
      videoFixture(1, 'deferred', 'completed', 'deferred'),
      videoFixture(2, 'failed', 'completed', 'failed'),
    ])
    renderView()
    await screen.findByText('Simulation evaluation was deferred.')
    expect(screen.getByText('Simulation evaluation failed.')).toBeInTheDocument()
    expect(within(screen.getByRole('table')).queryByText('Accepted')).not.toBeInTheDocument()
  })

  it('loads the run videos with the page-size cap and no filter initially', async () => {
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    expect(mockGetRunVideos).toHaveBeenCalledWith(7, { outcome: undefined, pageSize: 100 })
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(allVideos.length + 1)
  })

  it('filters the run videos by the selected outcome category', async () => {
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'excluded' } })

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: 'excluded', pageSize: 100 }),
    )
    const table = screen.getByRole('table')
    expect(within(table).getByText('Title v-excluded')).toBeInTheDocument()
    // Excluded is a category distinct from rejected and not-yet-evaluated.
    expect(within(table).getByText('Excluded')).toBeInTheDocument()
    expect(within(table).queryByText('Title v-rejected')).not.toBeInTheDocument()
    expect(within(table).queryByText('Title v-pending')).not.toBeInTheDocument()
    expect(within(table).queryByText('Not evaluated')).not.toBeInTheDocument()
  })

  it('keeps not-yet-evaluated distinct from rejected when filtered', async () => {
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'not_yet_evaluated' } })

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: 'not_yet_evaluated', pageSize: 100 }),
    )
    const table = screen.getByRole('table')
    expect(within(table).getByText('Title v-pending')).toBeInTheDocument()
    expect(within(table).queryByText('Title v-rejected')).not.toBeInTheDocument()
  })

  it('shows a filtered empty state, distinct from the unfiltered one, when nothing matches', async () => {
    // The run has no failed videos, so a failed filter matches nothing.
    serveVideos(allVideos.filter((v) => v.final_outcome !== 'failed'))
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'failed' } })

    await waitFor(() => expect(screen.getByText('No videos match the current filters.')).toBeInTheDocument())
    expect(screen.queryByText('No video outcomes yet.')).not.toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('keeps the unfiltered empty state when the run itself has no videos', async () => {
    serveVideos([])
    renderView()

    await waitFor(() => expect(screen.getByText('No video outcomes yet.')).toBeInTheDocument())
    expect(screen.queryByText('No videos match the current filters.')).not.toBeInTheDocument()
  })

  it('restores the full list when the filter is reset through the select', async () => {
    serveVideos(allVideos.filter((v) => v.final_outcome !== 'failed'))
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'failed' } })
    await waitFor(() => expect(screen.getByText('No videos match the current filters.')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: '' } })

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: undefined, pageSize: 100 }),
    )
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(screen.queryByText('No videos match the current filters.')).not.toBeInTheDocument()
  })

  it('resets the filter from the filtered empty state via the clear button', async () => {
    serveVideos(allVideos.filter((v) => v.final_outcome !== 'failed'))
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'failed' } })
    await waitFor(() => expect(screen.getByText('No videos match the current filters.')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: 'Clear outcome filter' }))

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: undefined, pageSize: 100 }),
    )
    expect(screen.getByRole('table')).toBeInTheDocument()
    expect(screen.queryByText('No videos match the current filters.')).not.toBeInTheDocument()
  })

  it('reports how many videos remain beyond the loaded page', async () => {
    mockGetRunVideos.mockResolvedValue({ videos: allVideos.slice(0, 2), total: 5 })
    renderView()

    await waitFor(() => expect(screen.getByText('Showing 2 of 5 videos.')).toBeInTheDocument())
  })

  it('shows a fetch error instead of a false filtered-empty state when the video fetch fails', async () => {
    mockGetRunVideos.mockRejectedValue(new Error('network down'))
    renderView()

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/Failed to load video outcomes/))
    expect(screen.queryByText('No videos match the current filters.')).not.toBeInTheDocument()
    expect(screen.queryByText('No video outcomes yet.')).not.toBeInTheDocument()
  })
})
