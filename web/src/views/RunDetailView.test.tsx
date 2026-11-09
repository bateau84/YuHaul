import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RunDetailView, RUN_POLL_INTERVAL_MS } from './RunDetailView'
import { ToastProvider } from '../components/ToastProvider'
import type { Run, RunStatus, VideoFinalOutcome, VideoOutcome } from '../types'

const mockGetRun = vi.fn()
const mockGetRunVideos = vi.fn()

vi.mock('../api/client', () => ({
  getRun: (...args: unknown[]) => mockGetRun(...args),
  getRunVideos: (...args: unknown[]) => mockGetRunVideos(...args),
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
    current_local_metadata: {
      attribution: 'current_local_not_necessarily_evaluated',
      title: `Title ${videoId}`, title_availability: 'current_local',
      channel_name: 'Channel One', channel_name_availability: 'current_local',
      duration_seconds: 0, duration_availability: 'current_local',
    },
    metadata_context_status: 'available',
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

// Server-side filter/pagination stand-in: mirrors GET /api/runs/:id/videos
// ?outcome=&page=&page_size=, including the filtered total and page bounds
// the empty state and paging controls depend on. Like the Go handler, an
// out-of-range page returns an empty slice rather than being clamped.
function serveVideos(videos: VideoOutcome[], pageSize = 100) {
  mockGetRunVideos.mockImplementation(
    (_id: number, query?: { outcome?: VideoFinalOutcome; page?: number; pageSize?: number }) => {
      const filtered = query?.outcome ? videos.filter((v) => v.final_outcome === query.outcome) : videos
      const size = query?.pageSize ?? pageSize
      const totalPages = Math.max(1, Math.ceil(filtered.length / size))
      const page = Math.max(1, query?.page ?? 1)
      const start = (page - 1) * size
      return Promise.resolve({
        videos: filtered.slice(start, start + size),
        total: filtered.length,
        totalPages,
      })
    },
  )
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
    mockGetRun.mockReset()
    mockGetRun.mockResolvedValue(runFixture)
    serveVideos(allVideos)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it.each([undefined, 'future_mode'])('gates results when run type is %s and retries context', async (runType) => {
    mockGetRun.mockResolvedValueOnce({ ...runFixture, run_type: runType })
      .mockResolvedValueOnce({ ...runFixture, run_type: 'backtest' })
    renderView()
    expect(await screen.findByRole('alert')).toHaveTextContent('Run context unavailable. Results cannot be interpreted safely.')
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByText('Run completed')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Hypothetical video outcomes')).toBeInTheDocument()
    expect(screen.getByText(/Simulation only/)).toBeInTheDocument()
    expect(mockGetRun).toHaveBeenCalledTimes(2)
  })

  it('shows every excluded route separately from the overall outcome', async () => {
    const route = { id: 'r1', route_id: 'r1', route_name: 'First', route_name_source: 'run_snapshot', evaluation_order: 0, decision: 'skipped' as const, skip_reason: 'exclusion' as const, causal_exclusion: { status: 'available', config_version_id: 42, exclusion_index: 2, semantics_version: 1, exclusion_type: 'word', field: 'title', comparison: 'lowercase_substring', configured_value: null, configured_value_status: 'withheld', matched: true, compared_input_status: 'unavailable' } }
    serveVideos([{ ...videoFixture(1, 'v', 'evaluated', 'rejected'), route_decisions: [route, { ...route, id: 'r2', route_id: 'r2', route_name_source: 'current_local', route_name: 'Second', causal_exclusion: null }] }])
    renderView()
    expect(await screen.findByText('Recorded causal exclusion: configuration 42, exclusion #3.')).toBeInTheDocument()
    expect(screen.getByText('Route: First (ID: r1)')).toBeInTheDocument()
    expect(screen.getByText('Route: Second (current/local name; ID: r2)')).toBeInTheDocument()
    expect(screen.getByText('Route exclusion recorded; rule unavailable. This explanation is incomplete.')).toBeInTheDocument()
    expect(within(screen.getByRole('table')).getByText('Rejected')).toBeInTheDocument()
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

  it('shows requested-run playlist uncertainty and authorization pending beside recorded outcomes', async () => {
    const uncertain = { ...videoFixture(11, 'guarded-video', 'failed', 'failed'), unresolved_mutations: [
      { action_type: 'add_to_playlist' as const, target_playlist_id: 'PLguard', application_state: 'unknown' as const },
    ] }
    const pending = { ...videoFixture(12, 'pending-video', 'deferred', 'deferred'), deferred_reason: 'authorization_pending' }
    serveVideos([uncertain, pending])
    renderView()
    await screen.findByText('Playlist outcome unknown')
    expect(screen.getByText('Waiting for YouTube access')).toBeInTheDocument()
    expect(screen.getAllByText('Failed').length).toBeGreaterThan(1)
    expect(screen.getAllByText('Deferred').length).toBeGreaterThan(1)
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

    expect(mockGetRunVideos).toHaveBeenCalledWith(7, { outcome: undefined, pageSize: 100, page: 1 })
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(allVideos.length + 1)
  })

  it('filters the run videos by the selected outcome category', async () => {
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'excluded' } })

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: 'excluded', pageSize: 100, page: 1 }),
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
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: 'not_yet_evaluated', pageSize: 100, page: 1 }),
    )
    const table = screen.getByRole('table')
    expect(within(table).getByText('Title v-pending')).toBeInTheDocument()
    expect(within(table).queryByText('Title v-rejected')).not.toBeInTheDocument()
  })

  it('keeps expired rows individually identifiable and retries only local list context', async () => {
    const first = videoFixture(21, 'expired-one', 'evaluated', 'excluded')
    const second = videoFixture(22, 'expired-two', 'evaluated', 'excluded')
    for (const video of [first, second]) {
      video.title = 'Old title is not display context'
      video.channel_name = 'Old channel is not display context'
      video.current_local_metadata = {
        attribution: 'current_local_not_necessarily_evaluated',
        title: null, title_availability: 'known_expired',
        channel_name: null, channel_name_availability: 'known_expired',
        duration_seconds: null, duration_availability: 'known_expired',
      }
    }
    const failed = { ...first, metadata_context_status: 'context_read_failed' }
    mockGetRunVideos.mockResolvedValueOnce({ videos: [failed, second], total: 2, totalPages: 1 })
      .mockResolvedValueOnce({ videos: [first, second], total: 2, totalPages: 1 })
    renderView()
    expect(await screen.findByText('Video ID: expired-one')).toBeInTheDocument()
    expect(screen.getByText('Video ID: expired-two')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Video title unavailable.*expired-one/ })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Video title unavailable.*expired-two/ })).toBeInTheDocument()
    expect(screen.getAllByText('Channel ID: c1')).toHaveLength(2)
    expect(getComputedStyle(screen.getByText('Video ID: expired-one')).display).toBe('block')
    expect(getComputedStyle(screen.getAllByText('Channel ID: c1')[0]).display).toBe('block')
    expect(screen.queryByText('Old title is not display context')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry local information' }))
    await waitFor(() => expect(mockGetRunVideos).toHaveBeenCalledTimes(2))
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

  it('explains a completed dry-run with a recorded empty selection', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      run_type: 'dry_run',
      status: 'completed',
      videos_selected: 0,
      videos_evaluated: 0,
    })
    serveVideos([])
    renderView()

    await screen.findByText('No videos were selected for this dry run.')
    expect(screen.getByText('Dry runs evaluate stored videos awaiting their first evaluation. They do not discover new videos or re-evaluate previously processed videos.')).toBeInTheDocument()
  })

  it('waits for the initial unfiltered outcomes response before claiming an empty selection', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      run_type: 'dry_run',
      status: 'completed',
      videos_selected: 0,
      videos_evaluated: 0,
    })
    let resolveVideos!: (result: { videos: VideoOutcome[]; total: number; totalPages: number }) => void
    mockGetRunVideos.mockReturnValue(new Promise((resolve) => { resolveVideos = resolve }))
    renderView()

    expect(await screen.findByText('Loading hypothetical video outcomes…')).toBeInTheDocument()
    expect(screen.queryByText('No videos were selected for this dry run.')).not.toBeInTheDocument()

    resolveVideos({ videos: [], total: 0, totalPages: 1 })
    await screen.findByText('No videos were selected for this dry run.')
  })

  it('shows a delayed initial outcomes failure instead of claiming an empty selection', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      run_type: 'dry_run',
      status: 'completed',
      videos_selected: 0,
      videos_evaluated: 0,
    })
    let rejectVideos!: (error: Error) => void
    mockGetRunVideos.mockReturnValue(new Promise((_resolve, reject) => { rejectVideos = reject }))
    renderView()

    expect(await screen.findByText('Loading hypothetical video outcomes…')).toBeInTheDocument()
    expect(screen.queryByText('No videos were selected for this dry run.')).not.toBeInTheDocument()

    rejectVideos(new Error('network down'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Failed to load hypothetical video outcomes')
    expect(screen.queryByText('No videos were selected for this dry run.')).not.toBeInTheDocument()
  })

  it.each([
    ['running', null, null, 'No hypothetical video outcomes recorded yet.'],
    ['completed', null, null, 'No hypothetical video outcomes were recorded for this run.'],
    ['failed', 0, 0, 'No hypothetical video outcomes were recorded for this run.'],
    ['interrupted', 0, 0, 'No hypothetical video outcomes were recorded for this run.'],
  ] as const)('keeps empty dry-run copy neutral for %s with selection %s and evaluation %s', async (status, selected, evaluated, copy) => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      run_type: 'dry_run',
      status,
      videos_selected: selected,
      videos_evaluated: evaluated,
    })
    serveVideos([])
    renderView()

    await screen.findByText(copy)
    expect(screen.queryByText('No videos were selected for this dry run.')).not.toBeInTheDocument()
  })

  it('restores the full list when the filter is reset through the select', async () => {
    serveVideos(allVideos.filter((v) => v.final_outcome !== 'failed'))
    renderView()
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'failed' } })
    await waitFor(() => expect(screen.getByText('No videos match the current filters.')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: '' } })

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: undefined, pageSize: 100, page: 1 }),
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
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: undefined, pageSize: 100, page: 1 }),
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

describe('RunDetailView lifecycle (Task 2.1)', () => {
  function deferred<T>() {
    let resolve!: (value: T) => void
    const promise = new Promise<T>((resolvePromise) => { resolve = resolvePromise })
    return { promise, resolve }
  }

  // Deterministic async control under fake timers (see RunsView tests): the
  // clock only moves when the test says so, and `flush` drains the promise
  // chain, so no assertion depends on wall-clock auto-advance.
  async function flush() {
    await act(async () => {
      for (let i = 0; i < 25; i++) await Promise.resolve()
      await vi.advanceTimersByTimeAsync(0)
    })
  }

  function advance(ms: number) {
    return act(async () => {
      await vi.advanceTimersByTimeAsync(ms)
      for (let i = 0; i < 25; i++) await Promise.resolve()
    })
  }

  beforeEach(() => {
    vi.clearAllMocks()
    mockGetRun.mockReset()
    mockGetRun.mockResolvedValue(runFixture)
    serveVideos(allVideos)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps polling a running run until the stored status turns terminal, then stops', async () => {
    vi.useFakeTimers()
    mockGetRun
      .mockResolvedValueOnce({ ...runFixture, status: 'running', current_phase: 'discovery', videos_selected: null, videos_discovered: 0 })
      .mockResolvedValueOnce({ ...runFixture, status: 'failed', interrupt_reason: 'quota exhausted' })

    renderView()
    await flush()
    expect(screen.getByText(/Run in progress/)).toBeInTheDocument()
    expect(screen.getByText('Run in progress — Current phase: Discovering videos')).toBeInTheDocument()
    expect(screen.getByText(/there is no known completion total/)).toBeInTheDocument()
    expect(screen.queryByText('Run failed')).not.toBeInTheDocument()

    await advance(RUN_POLL_INTERVAL_MS)

    // Terminal display follows the stored state, with the safe run-level
    // failure reason — no success toast from the earlier "running" phase.
    expect(screen.getByText('Run failed')).toBeInTheDocument()
    expect(screen.getByText('Reason: quota exhausted')).toBeInTheDocument()
    expect(screen.queryByText(/Run in progress/)).not.toBeInTheDocument()

    const readsAfterTerminal = mockGetRun.mock.calls.length
    await advance(RUN_POLL_INTERVAL_MS * 2)
    expect(mockGetRun.mock.calls.length).toBe(readsAfterTerminal)
  })

  it('coalesces slow status polling so an in-flight terminal read is not invalidated', async () => {
    vi.useFakeTimers()
    const running = { ...runFixture, status: 'running' as RunStatus, current_phase: 'discovery' }
    const slowTerminal = deferred<Run | undefined>()
    const unusedFollowup = deferred<Run | undefined>()
    mockGetRun
      .mockResolvedValueOnce(running)
      .mockReturnValueOnce(slowTerminal.promise)
      .mockReturnValueOnce(unusedFollowup.promise)

    renderView()
    await flush()
    expect(screen.getByText(/Run in progress/)).toBeInTheDocument()

    await advance(RUN_POLL_INTERVAL_MS)
    expect(mockGetRun).toHaveBeenCalledTimes(2)
    // Simulate an API read taking longer than the poll interval. The next
    // interval must coalesce instead of starting a newer invalidating read.
    await advance(RUN_POLL_INTERVAL_MS)
    expect(mockGetRun).toHaveBeenCalledTimes(2)

    await act(async () => {
      slowTerminal.resolve({ ...runFixture, status: 'failed', current_phase: 'discovery' })
      await Promise.resolve()
      await Promise.resolve()
    })
    expect(screen.getByText('Run failed')).toBeInTheDocument()
    expect(screen.getByText('Last recorded phase: Discovering videos')).toBeInTheDocument()
  })

  it('shows an unknown status with a visible retry when the initial status read fails', async () => {
    mockGetRun.mockRejectedValueOnce(new Error('network down'))

    renderView()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent("Failed to load run status. The run's current state is unknown")
    expect(screen.queryByText(/Run in progress/)).not.toBeInTheDocument()
    expect(screen.queryByText('Run completed')).not.toBeInTheDocument()

    // Retry recovers from the read failure.
    mockGetRun.mockResolvedValue(runFixture)
    fireEvent.click(within(alert).getByRole('button', { name: /^Retry$/ }))
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: 'Run 7' })).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('reports a missing run instead of loading forever', async () => {
    mockGetRun.mockResolvedValue(undefined)

    renderView()
    expect(await screen.findByText('Run 7 was not found.')).toBeInTheDocument()
    expect(screen.queryByText('Loading run...')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Back to runs' })).toBeInTheDocument()
  })

  it('keeps the last stored status visible with a retry when a background refresh fails', async () => {
    vi.useFakeTimers()
    const running = { ...runFixture, status: 'running' as RunStatus, current_phase: 'routing' }
    mockGetRun
      .mockResolvedValueOnce(running)
      .mockRejectedValueOnce(new Error('network down'))

    renderView()
    await flush()
    expect(screen.getByText('Run in progress — Current phase: Evaluating routes')).toBeInTheDocument()

    await advance(RUN_POLL_INTERVAL_MS)

    // The read failure does not become completion and does not clear the
    // last known status; it shows a visible retry instead.
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Could not refresh status. This run may still be processing. Last known status: running.')
    expect(screen.getByText('Run in progress — Last known phase: Evaluating routes')).toBeInTheDocument()
    expect(screen.queryByText('Run completed')).not.toBeInTheDocument()

    mockGetRun.mockResolvedValue({ ...runFixture, status: 'failed' })
    fireEvent.click(within(alert).getByRole('button', { name: /^Retry$/ }))
    await flush()
    expect(screen.getByText('Run failed')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows interrupted with its safe reason on the run itself', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      status: 'interrupted',
      interrupt_reason: 'process restarted',
    })

    renderView()
    expect(await screen.findByText('Run interrupted')).toBeInTheDocument()
    expect(screen.getByText('Reason: process restarted')).toBeInTheDocument()
  })

  it('keeps a readable safe phase after failure', async () => {
    mockGetRun.mockResolvedValue({ ...runFixture, status: 'failed', current_phase: 'action_execution' })
    renderView()
    expect(await screen.findByText('Last recorded phase: Processing playlist actions')).toBeInTheDocument()
  })

  it('does not expose an unrecognized raw phase string', async () => {
    mockGetRun.mockResolvedValue({ ...runFixture, status: 'running', current_phase: 'future_internal_phase' })
    renderView()
    expect(await screen.findByText('Run in progress — Current phase unavailable.')).toBeInTheDocument()
    expect(screen.queryByText(/future_internal_phase/)).not.toBeInTheDocument()
  })

  it('marks a withheld failure reason as unavailable instead of leaking or omitting it', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      status: 'failed',
      interrupt_reason: null,
      failure_reason_unavailable: true,
    })

    renderView()
    expect(await screen.findByText('Run failed')).toBeInTheDocument()
    expect(
      screen.getByText('A reason was recorded for this run, but it cannot be displayed safely.'),
    ).toBeInTheDocument()
    expect(screen.queryByText(/No failure reason was recorded/)).not.toBeInTheDocument()
  })

  it('says no failure reason was recorded when a failed run has neither a reason nor a flag', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      status: 'failed',
      interrupt_reason: null,
    })

    renderView()
    expect(await screen.findByText('Run failed')).toBeInTheDocument()
    expect(screen.getByText('No failure reason was recorded for this run.')).toBeInTheDocument()
  })
})

describe('RunDetailView counts (BR-020 / ARD-001)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetRun.mockReset()
    mockGetRun.mockResolvedValue(runFixture)
    serveVideos(allVideos)
  })

  // Reads the number/label inside the summary card that owns `label`, so an
  // assertion can never be satisfied by a different card or by prose.
  function metricValue(label: string): string {
    const labelEl = screen.getByText(label, { selector: '.metric-label' })
    return labelEl.closest('.card')!.querySelector('.metric-value')!.textContent ?? ''
  }

  it('shows selected as Unknown when never captured, 0 only when captured empty', async () => {
    mockGetRun.mockResolvedValue({ ...runFixture, videos_selected: null, videos_evaluated: 3 })

    renderView()
    await waitFor(() => expect(screen.getByText('Selected')).toBeInTheDocument())
    // null → Unknown, never 0.
    expect(metricValue('Selected')).toBe('Unknown')
    // Evaluated is the decision-bearing count.
    expect(metricValue('Evaluated')).toBe('3')
    // Discovered remains its own production count.
    expect(metricValue('Discovered')).toBe('6')
  })

  it('shows a captured empty selection as 0', async () => {
    mockGetRun.mockResolvedValue({ ...runFixture, videos_selected: 0, videos_evaluated: 0 })

    renderView()
    await waitFor(() => expect(screen.getByText('Selected')).toBeInTheDocument())
    expect(metricValue('Selected')).toBe('0')
  })

  it('does not reuse production videos_discovered as the simulation selected or evaluated count', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      run_type: 'backtest',
      videos_discovered: 42,
      videos_selected: 14,
      videos_evaluated: 13,
    })

    renderView()
    await waitFor(() => expect(screen.getByText('Hypothetical results')).toBeInTheDocument())

    // Simulation shows its own selected/evaluated inputs, not discovered.
    expect(metricValue('Selected')).toBe('14')
    expect(metricValue('Evaluated videos')).toBe('13')
    expect(screen.queryByText('Discovered', { selector: '.metric-label' })).not.toBeInTheDocument()
    // 42 (production discovered) is not shown anywhere in the summary.
    expect(screen.queryByText('42')).not.toBeInTheDocument()
  })

  it('shows an em dash for a legacy run without an evaluated count instead of a number', async () => {
    mockGetRun.mockResolvedValue({
      ...runFixture,
      videos_selected: undefined,
      videos_evaluated: undefined,
    })

    renderView()
    await waitFor(() => expect(screen.getByText('Selected')).toBeInTheDocument())
    expect(metricValue('Selected')).toBe('Unknown')
    expect(metricValue('Evaluated')).toBe('—')
  })
})

describe('RunDetailView pagination (ARS-001)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockGetRun.mockReset()
    mockGetRun.mockResolvedValue(runFixture)
  })

  function manyVideos(count: number): VideoOutcome[] {
    return Array.from({ length: count }, (_, i) =>
      videoFixture(i + 1, `v-${i + 1}`, 'completed', 'accepted'),
    )
  }

  it('exposes every page of a run with more than 100 videos', async () => {
    serveVideos(manyVideos(150))
    renderView()

    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
    // Page 1 holds the 100-item cap.
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(101)
    expect(screen.getByText('Page 1 of 2')).toBeInTheDocument()
    expect(screen.getByText('Showing 1–100 of 150 videos.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
    expect(mockGetRunVideos).toHaveBeenCalledWith(7, { outcome: undefined, pageSize: 100, page: 1 })

    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: undefined, pageSize: 100, page: 2 }),
    )
    await waitFor(() => expect(screen.getByText('Page 2 of 2')).toBeInTheDocument())
    // The second page shows the remaining 50 rows, not an empty list.
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(51)
    expect(screen.getByText('Showing 101–150 of 150 videos.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'Previous page' })).toBeEnabled()
  })

  it('resets to page 1 when the outcome filter changes and keeps filtered totals', async () => {
    serveVideos(manyVideos(150))
    renderView()
    await waitFor(() => expect(screen.getByText('Page 1 of 2')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    await waitFor(() => expect(screen.getByText('Page 2 of 2')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText('Outcome'), { target: { value: 'accepted' } })

    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: 'accepted', pageSize: 100, page: 1 }),
    )
    expect(screen.getByText('Page 1 of 2')).toBeInTheDocument()
    expect(screen.getByText('Showing 1–100 of 150 videos.')).toBeInTheDocument()
  })

  it('clamps to the last available page when the current page falls outside the server total', async () => {
    // First read reports 2 pages; after the user advances, the server says
    // only 1 page remains (run changed underneath).
    mockGetRunVideos
      .mockResolvedValueOnce({ videos: manyVideos(100), total: 150, totalPages: 2 })
      .mockResolvedValueOnce({ videos: [], total: 6, totalPages: 1 })
      .mockResolvedValue({ videos: allVideos, total: 6, totalPages: 1 })

    renderView()
    await waitFor(() => expect(screen.getByText('Page 1 of 2')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))

    // Instead of a false "No video outcomes yet", it re-reads the last page.
    await waitFor(() =>
      expect(mockGetRunVideos).toHaveBeenLastCalledWith(7, { outcome: undefined, pageSize: 100, page: 1 }),
    )
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
    expect(screen.queryByText('No video outcomes yet.')).not.toBeInTheDocument()
    expect(within(screen.getByRole('table')).getAllByRole('row')).toHaveLength(7)
  })
})
