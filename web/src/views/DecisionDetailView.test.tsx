import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route, Link } from 'react-router-dom'
import { DecisionDetailView } from './DecisionDetailView'
import { ToastProvider } from '../components/ToastProvider'
import type { DecisionDetail } from '../types'

const mockGetDecisionDetail = vi.fn()
const mockGetRun = vi.fn()

vi.mock('../api/client', () => ({
  getDecisionDetail: (...args: [number, string]) => mockGetDecisionDetail(...args),
  getRun: (...args: unknown[]) => mockGetRun(...args),
}))

const ROUTE_A = '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce'
const ROUTE_B = '4c7c3b7e-f09e-4bf4-9b55-aec0cf479b51'

const baseDetail: DecisionDetail = {
  video: {
    id: 1001,
    video_id: 'dQw4w9WgXcQ',
    channel_id: 'UCdemo1',
    title: 'Late Night Jazz Mix',
    channel_name: 'Jazz Club',
    duration_seconds: 184,
    current_local_metadata: {
      attribution: 'current_local_not_necessarily_evaluated',
      title: 'Late Night Jazz Mix', title_availability: 'current_local',
      channel_name: 'Jazz Club', channel_name_availability: 'current_local',
      duration_seconds: 184, duration_availability: 'current_local',
    },
    metadata_context_status: 'available',
    processing_state: 'completed',
    final_outcome: 'accepted',
  },
  processing_state: 'completed',
  final_outcome: 'accepted',
  route_decisions: [
    {
      id: ROUTE_A,
      route_id: ROUTE_A,
      route_name: 'Music Discovery',
      evaluation_order: 1,
      decision: 'matched',
      condition_audits: [
        { id: 0, condition_index: 0, field: 'title', operator: 'contains', expected_value: 'music', actual_value: 'Late Night Jazz Mix', matched: true },
        { id: 1, condition_index: 1, field: 'duration_seconds', operator: 'gt', expected_value: '180', actual_value: '184', matched: true },
      ],
      actions: [{ id: 0, action_type: 'add_to_playlist', target_playlist_id: 'PLmusic', outcome: 'succeeded' }],
    },
    {
      id: ROUTE_B,
      route_id: ROUTE_B,
      route_name: 'Ambient',
      evaluation_order: 2,
      decision: 'skipped',
      skip_reason: 'final_route_matched',
      condition_audits: [],
      actions: [],
    },
  ],
}

describe('DecisionDetailView', () => {
  beforeEach(() => {
    mockGetRun.mockReset().mockResolvedValue({ id: 1, run_type: 'manual' })
    mockGetDecisionDetail.mockReset()
  })
  function renderView(runId: string, videoId: string) {
    return render(
      <MemoryRouter initialEntries={[`/runs/${runId}/video/${videoId}`]}>
        <ToastProvider>
          <Routes>
            <Route path="/runs/:id/video/:videoId" element={<DecisionDetailView />} />
          </Routes>
        </ToastProvider>
      </MemoryRouter>,
    )
  }

  it('renders the three-layer evidence model for an accepted video', async () => {
    mockGetDecisionDetail.mockResolvedValue(baseDetail)
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(screen.getByText('Layer 1 — Processing outcome')).toBeInTheDocument()
    expect(screen.getByText('Accepted')).toBeInTheDocument()

    expect(screen.getByText('Layer 2 — Route decisions')).toBeInTheDocument()
    expect(screen.getAllByText('Music Discovery').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Matched')).toBeInTheDocument()
    expect(screen.getByText('Conditions evaluated')).toBeInTheDocument()
    expect(screen.getByText('Actions')).toBeInTheDocument()

    expect(screen.getByText('Layer 3 — Action outcomes')).toBeInTheDocument()
    expect(screen.getAllByText('Succeeded').length).toBeGreaterThanOrEqual(1)
    // Public action representations use the canonical API action type (ADR-007).
    expect(screen.getAllByText('add_to_playlist → PLmusic').length).toBeGreaterThanOrEqual(1)
    expect(screen.getByText('Source channel: Jazz Club')).toBeInTheDocument()
    // One attribution notice per video context block, not one per field.
    expect(screen.getAllByText(/Current\/local information/)).toHaveLength(1)
    expect(screen.queryByText('Current/local information')).not.toBeInTheDocument()
    expect(screen.getByText('Current/local information — not necessarily the information evaluated by this run.')).toBeInTheDocument()
    expect(screen.queryByText(/^Observed at /)).not.toBeInTheDocument()
  })

  it('shows unresolved playlist application as unknown without inventing an attempt', async () => {
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      video: {
        ...baseDetail.video,
        unresolved_mutations: [{ action_type: 'add_to_playlist', target_playlist_id: 'PLuncertain', prepared_at: '2026-11-07T10:00:00Z', application_state: 'unknown' }],
      },
      route_decisions: [{
        ...baseDetail.route_decisions[0],
        actions: [{ id: 1, action_type: 'add_to_playlist', target_playlist_id: 'PLpending', outcome: 'deferred', outcome_reason: 'authorization_pending' }],
      }],
    })
    renderView('1', 'dQw4w9WgXcQ')
    await screen.findAllByText('Waiting for YouTube access')
    expect(screen.getByText('Playlist outcome unknown')).toBeInTheDocument()
    expect(screen.queryByText('Not attempted — unresolved playlist action')).not.toBeInTheDocument()
    expect(screen.getByText('YuHaul cannot confirm whether this playlist action was sent or applied. Repeating it could add the video again, so it remains blocked.')).toBeInTheDocument()
    expect(screen.getByText('Playlist PLuncertain')).toBeInTheDocument()
    expect(screen.getByText(/Prepared .* — not an attempt time\./)).toBeInTheDocument()
    expect(screen.queryByText(/Attempted /)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /retry|replay/i })).not.toBeInTheDocument()
  })

  it('does not claim a guard-only playlist action was or was not sent', async () => {
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      video: {
        ...baseDetail.video,
        unresolved_mutations: [{ action_type: 'add_to_playlist', target_playlist_id: 'PLguard', application_state: 'unknown' }],
      },
      route_decisions: [],
    })
    renderView('1', 'dQw4w9WgXcQ')

    expect(await screen.findByText('Playlist outcome unknown')).toBeInTheDocument()
    expect(screen.getByText('YuHaul cannot confirm whether this playlist action was sent or applied. Repeating it could add the video again, so it remains blocked.')).toBeInTheDocument()
    expect(screen.queryByText(/Not attempted/)).not.toBeInTheDocument()
    expect(screen.getByText('An unresolved playlist action is recorded above; no action outcome was recorded.')).toBeInTheDocument()
  })

  it.each(['backtest', 'replay', 'dry_run'] as const)('preserves %s context on a direct video URL', async (run_type) => {
    mockGetRun.mockResolvedValue({ id: 1, run_type })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      processing_state: 'evaluated',
      route_decisions: baseDetail.route_decisions.map((d) => ({
        ...d, actions: d.actions?.map((a) => ({ ...a, outcome: 'not_attempted' })),
      })),
    })
    renderView('1', 'dQw4w9WgXcQ')
    await screen.findByText('Hypothetical accepted')
    const title = run_type === 'dry_run' ? 'Dry-run' : run_type === 'backtest' ? 'Backtest' : 'Replay'
    await waitFor(() => expect(screen.getByRole('heading', { level: 1, name: `${title} decision detail` })).toHaveFocus())
    expect(mockGetRun).toHaveBeenCalledWith(1)
    expect(screen.getByText('Run 1')).toBeInTheDocument()
    expect(screen.getByText('Simulated actions')).toBeInTheDocument()
    expect(screen.getByText('Layer 3 — Simulated action outcomes')).toBeInTheDocument()
    expect(screen.getAllByText('Not attempted')).toHaveLength(2)
    expect(screen.queryByText(/Processing state:/)).not.toBeInTheDocument()
    expect(screen.queryByText('Actions executed')).not.toBeInTheDocument()
  })

  it('shows requested-run rejection and empty simulated actions without production annotations', async () => {
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'backtest' })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      video: { ...baseDetail.video, deferred_reason: 'production deferral', error_message: 'production error' },
      final_outcome: 'rejected',
      route_decisions: [],
    })
    renderView('1', 'dQw4w9WgXcQ')
    await screen.findByText('Hypothetical rejected')
    expect(screen.getByText('No simulated actions recorded for this video.')).toBeInTheDocument()
    expect(screen.queryByText(/production deferral|production error/)).not.toBeInTheDocument()
    expect(screen.queryByText('Accepted')).not.toBeInTheDocument()
  })

  // Requested-run production evidence (ARD-001): a real attempt shows its
  // outcome, safe error text, and the attempt timestamp — and only an
  // actually attempted action carries a timestamp.
  it('shows a failed production action with its safe error message and attempt timestamp', async () => {
    const executedAt = '2026-10-27T09:30:00Z'
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'manual' })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      route_decisions: [
        {
          ...baseDetail.route_decisions[0],
          actions: [
            {
              id: 0,
              action_type: 'add_to_playlist',
              target_playlist_id: 'PLmusic',
              outcome: 'failed',
              error_message: 'HTTP 500 quotaExceeded',
              executed_at: executedAt,
            },
          ],
        },
        baseDetail.route_decisions[1],
      ],
    })
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(screen.getAllByText('Failed').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('HTTP 500 quotaExceeded').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText(`Attempted ${new Date(executedAt).toLocaleString()}`).length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/Succeeded/)).not.toBeInTheDocument()
  })

  // already_completed is an idempotency suppression in THIS run: the prior
  // success is not a current-run success and has no attempt timestamp.
  it('presents already_completed as not attempted in this run, never as a success', async () => {
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'manual' })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      route_decisions: [
        {
          ...baseDetail.route_decisions[0],
          actions: [
            {
              id: 0,
              action_type: 'add_to_playlist',
              target_playlist_id: 'PLmusic',
              outcome: 'not_attempted',
              outcome_reason: 'already_completed',
            },
          ],
        },
        baseDetail.route_decisions[1],
      ],
    })
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(screen.getAllByText('Not attempted').length).toBeGreaterThanOrEqual(1)
    expect(
      screen.getAllByText('Not attempted in this run — already completed by an earlier run').length,
    ).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/Succeeded/)).not.toBeInTheDocument()
    // No attempt was made in this run, so no attempt timestamp appears.
    expect(screen.queryByText(/Attempted/)).not.toBeInTheDocument()
  })

  // A quota deferral is an explanation of a recorded outcome, with no
  // external attempt and therefore no attempt timestamp.
  it('shows a deferred action reason without an attempt timestamp', async () => {
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'manual' })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      route_decisions: [
        {
          ...baseDetail.route_decisions[0],
          actions: [
            {
              id: 0,
              action_type: 'add_to_playlist',
              target_playlist_id: 'PLmusic',
              outcome: 'deferred',
              outcome_reason: 'quota_exhausted',
            },
          ],
        },
        baseDetail.route_decisions[1],
      ],
    })
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(screen.getAllByText('Deferred').length).toBeGreaterThanOrEqual(1)
    expect(
      screen.getAllByText('Not attempted — the daily quota was exhausted before this action could run').length,
    ).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/Attempted/)).not.toBeInTheDocument()
  })

  // Withheld legacy error text is stated as unavailable, never rendered raw
  // and never treated as a fabricated success or rejection.
  it('marks a withheld action error reason as unavailable instead of showing raw text', async () => {
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'manual' })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      route_decisions: [
        {
          ...baseDetail.route_decisions[0],
          actions: [
            {
              id: 0,
              action_type: 'add_to_playlist',
              target_playlist_id: 'PLmusic',
              outcome: 'failed',
              error_message_unavailable: true,
            },
          ],
        },
        baseDetail.route_decisions[1],
      ],
    })
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(
      screen.getAllByText('An error reason was recorded for this action, but it cannot be displayed safely.').length,
    ).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText(/Succeeded/)).not.toBeInTheDocument()
  })

  // Video-level withheld legacy text (Layer 1) is stated as unavailable and
  // never rendered raw or inferred as a failure/success of its own.
  it('marks a withheld video error reason as unavailable on Layer 1', async () => {
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'manual' })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      video: { ...baseDetail.video, error_message_unavailable: true },
    })
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(
      screen.getByText('An error reason was recorded for this video, but it cannot be displayed safely.'),
    ).toBeInTheDocument()
  })

  // Simulation rows are hypothetical even against adversarial input: a stored
  // outcome/timestamp from a simulation row must never read as execution
  // success or as an attempt (ARD-001 / Simulation Inspection).
  it('never presents a simulation action as executed or succeeded', async () => {
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'backtest' })
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      route_decisions: [
        {
          ...baseDetail.route_decisions[0],
          actions: [
            {
              id: 0,
              action_type: 'add_to_playlist',
              target_playlist_id: 'PLmusic',
              outcome: 'succeeded',
              executed_at: '2026-10-27T09:30:00Z',
              error_message: 'provider error text',
            },
          ],
        },
        baseDetail.route_decisions[1],
      ],
    })
    renderView('1', 'dQw4w9WgXcQ')
    await screen.findByText('Hypothetical accepted')

    expect(screen.getAllByText('Not attempted').length).toBeGreaterThanOrEqual(1)
    expect(screen.queryByText('Succeeded')).not.toBeInTheDocument()
    expect(screen.queryByText(/Attempted/)).not.toBeInTheDocument()
    expect(screen.queryByText('provider error text')).not.toBeInTheDocument()
    expect(
      screen.getByText("These actions were not executed. Accepted means the video matched this simulation's routing rules, not that a playlist was changed."),
    ).toBeInTheDocument()
  })

  it('does not interpret results when the run type is unsupported', async () => {
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'future_mode' })
    mockGetDecisionDetail.mockResolvedValue(baseDetail)
    renderView('1', 'dQw4w9WgXcQ')
    expect(await screen.findByRole('alert')).toHaveTextContent('Run context unavailable.')
    expect(screen.queryByText('Accepted')).not.toBeInTheDocument()
    expect(screen.queryByText('Actions')).not.toBeInTheDocument()
  })

  it('withholds interpretation until context loads and supports failure retry', async () => {
    let rejectContext!: (error: Error) => void
    mockGetRun.mockReturnValueOnce(new Promise((_resolve, reject) => { rejectContext = reject }))
    mockGetDecisionDetail.mockResolvedValue(baseDetail)
    renderView('1', 'dQw4w9WgXcQ')
    expect(screen.getByText('Loading run context…')).toBeInTheDocument()
    expect(screen.queryByText('Accepted')).not.toBeInTheDocument()
    rejectContext(new Error('offline'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Run context unavailable. Results cannot be interpreted safely.')
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'replay' })
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await screen.findByText('Hypothetical accepted')
  })

  it('shows a read error and retries the detail request without implying an outcome', async () => {
    mockGetDecisionDetail.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(baseDetail)
    renderView('1', 'dQw4w9WgXcQ')
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load decision detail.')
    expect(screen.queryByText('Accepted')).not.toBeInTheDocument()
    expect(screen.queryByText(/Rejected/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await screen.findByText('Late Night Jazz Mix')
    expect(mockGetDecisionDetail).toHaveBeenCalledTimes(2)
  })

  it('shows not-found as unavailable evidence, not a routing result', async () => {
    mockGetDecisionDetail.mockResolvedValue(undefined)
    renderView('1', 'missing-video')
    expect(await screen.findByRole('alert')).toHaveTextContent('Decision detail not found.')
    expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to run' })).toHaveAttribute('href', '/runs/1')
    expect(screen.queryByText(/Accepted|Rejected|Succeeded/)).not.toBeInTheDocument()
  })

  it('ignores a stale detail response after navigating to another video', async () => {
    let resolveOld!: (value: DecisionDetail) => void
    mockGetDecisionDetail.mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve }))
      .mockResolvedValueOnce({ ...baseDetail, video: {
        ...baseDetail.video, video_id: 'new-video', title: 'New video',
        current_local_metadata: { ...baseDetail.video.current_local_metadata!, title: 'New video' },
      } })
    render(
      <MemoryRouter initialEntries={['/runs/1/video/old-video']}>
        <ToastProvider><Link to="/runs/1/video/new-video">Next video</Link><Routes>
          <Route path="/runs/:id/video/:videoId" element={<DecisionDetailView />} />
        </Routes></ToastProvider>
      </MemoryRouter>,
    )
    fireEvent.click(screen.getByRole('link', { name: 'Next video' }))
    await screen.findByText('New video')
    resolveOld({ ...baseDetail, video: { ...baseDetail.video, title: 'Stale video' } })
    expect(screen.queryByText('Stale video')).not.toBeInTheDocument()
  })

  it('shows skipped route reason after a final route matched', async () => {
    mockGetDecisionDetail.mockResolvedValue(baseDetail)
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(screen.getByText('Skipped')).toBeInTheDocument()
    expect(screen.getByText(/A prior final route already matched this video/)).toBeInTheDocument()
  })

  it('shows condition mismatch for a rejected video', async () => {
    const rejectedDetail: DecisionDetail = {
      video: {
        id: 1002,
        video_id: 'abc123',
        channel_id: 'UCdemo2',
        title: 'Ambient Study Session',
        channel_name: 'Focus Sounds',
        duration_seconds: 620,
        current_local_metadata: {
          attribution: 'current_local_not_necessarily_evaluated',
          title: 'Ambient Study Session', title_availability: 'current_local',
          channel_name: 'Focus Sounds', channel_name_availability: 'current_local',
          duration_seconds: 620, duration_availability: 'current_local',
        },
        processing_state: 'evaluated',
        final_outcome: 'rejected',
      },
      processing_state: 'evaluated',
      final_outcome: 'rejected',
      route_decisions: [
        {
          id: ROUTE_A,
          route_id: ROUTE_A,
          route_name: 'Music Discovery',
          evaluation_order: 1,
          decision: 'not_matched',
          rejection_reason: 'title does not contain "music"',
          condition_audits: [
            { id: 0, condition_index: 0, field: 'title', operator: 'contains', expected_value: 'music', actual_value: 'Ambient Study Session', matched: false },
            { id: 1, condition_index: 1, field: 'duration_seconds', operator: 'gt', expected_value: '180', actual_value: '620', matched: true },
          ],
          actions: [],
        },
        {
          id: ROUTE_B,
          route_id: ROUTE_B,
          route_name: 'Ambient',
          evaluation_order: 2,
          decision: 'skipped',
          skip_reason: 'disabled',
          condition_audits: [],
          actions: [],
        },
      ],
    }
    mockGetDecisionDetail.mockResolvedValue(rejectedDetail)
    renderView('1', 'abc123')
    await waitFor(() => expect(screen.getByText('Ambient Study Session')).toBeInTheDocument())

    expect(screen.getByText('Did not match')).toBeInTheDocument()
    expect(screen.getByText(/title does not contain/)).toBeInTheDocument()
    expect(screen.getByText(/This route is currently disabled/)).toBeInTheDocument()
  })

  // Two routes may share a display name (BR-003 AC10); the decision rows must
  // stay distinguishable by their short UUID suffix.
  it('distinguishes same-named routes by short UUID suffix in decision rows and action outcomes', async () => {
    const ROUTE_C = 'b7e9d4f2-1c2a-4d3b-8e5f-9a0b1c2d3e4f'
    const duplicateDetail: DecisionDetail = {
      ...baseDetail,
      route_decisions: [
        {
          id: ROUTE_A,
          route_id: ROUTE_A,
          route_name: 'Music Discovery',
          evaluation_order: 1,
          decision: 'matched',
          condition_audits: [],
          actions: [{ id: 0, action_type: 'add_to_playlist', target_playlist_id: 'PLmusic', outcome: 'succeeded' }],
        },
        {
          id: ROUTE_C,
          route_id: ROUTE_C,
          route_name: 'Music Discovery',
          evaluation_order: 2,
          decision: 'matched',
          condition_audits: [],
          actions: [{ id: 0, action_type: 'add_to_playlist', target_playlist_id: 'PLother', outcome: 'succeeded' }],
        },
      ],
    }
    mockGetDecisionDetail.mockResolvedValue(duplicateDetail)
    renderView('1', 'dQw4w9WgXcQ')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    // Layer 2 decision rows show both same-named routes with distinct suffixes.
    expect((await screen.findAllByText('(3a554e96)')).length).toBeGreaterThanOrEqual(1)
    expect((await screen.findAllByText('(b7e9d4f2)')).length).toBeGreaterThanOrEqual(1)
    // Layer 3 attributes each action line to the right same-named route: only
    // the Layer 3 outcome rows pair the UUID suffix with the playlist target.
    const outcomeRows = Array.from(document.querySelectorAll('li')).filter(
      (li) =>
        li.textContent?.includes('add_to_playlist') &&
        /\([0-9a-f]{8}\)/.test(li.textContent ?? '')
    )
    expect(outcomeRows).toHaveLength(2)
    const rowFor = (suffix: string) =>
      outcomeRows.find((li) => li.textContent?.includes(suffix))
    expect(rowFor('(3a554e96)')?.textContent).toContain('PLmusic')
    expect(rowFor('(b7e9d4f2)')?.textContent).toContain('PLother')
  })

  it('does not present legacy names when current/local metadata is missing', async () => {
    mockGetDecisionDetail.mockResolvedValue({ ...baseDetail, video: { ...baseDetail.video, current_local_metadata: undefined } })
    renderView('1', 'dQw4w9WgXcQ')
    expect(await screen.findByText('Video title unavailable')).toBeInTheDocument()
    expect(screen.getByText('Source channel name unavailable')).toBeInTheDocument()
    expect(screen.getByText('UCdemo1')).toBeInTheDocument()
    expect(screen.getByText('Duration unavailable')).toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Late Night Jazz Mix' })).not.toBeInTheDocument()
  })

  it.each(['known_expired', 'withheld'] as const)('shows %s availability without exposing a legacy value', async (availability) => {
    mockGetDecisionDetail.mockResolvedValue({ ...baseDetail, video: {
      ...baseDetail.video,
      current_local_metadata: {
        ...baseDetail.video.current_local_metadata!, title: null, title_availability: availability,
        channel_name: null, channel_name_availability: availability,
        duration_seconds: null, duration_availability: availability,
      },
    } })
    renderView('1', 'dQw4w9WgXcQ')
    const expected = availability === 'known_expired'
      ? ['Video title unavailable — local metadata expired', 'Source channel name unavailable — local metadata expired', 'Duration unavailable — local metadata expired']
      : ['Video title unavailable for display', 'Source channel name unavailable for display', 'Duration unavailable for display']
    for (const label of expected) expect(await screen.findByText(label)).toBeInTheDocument()
    expect(screen.queryByText(/^Observed at /)).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Late Night Jazz Mix' })).not.toBeInTheDocument()
  })

  it('shows current duration zero as known and distinguishes metadata read failure', async () => {
    mockGetDecisionDetail.mockResolvedValue({ ...baseDetail, video: {
      ...baseDetail.video,
      metadata_context_status: 'context_read_failed',
      current_local_metadata: {
        ...baseDetail.video.current_local_metadata!,
        source_fetched_at: '2026-11-07T10:15:30Z',
        title: null, title_availability: 'unknown',
        channel_name: null, channel_name_availability: 'unknown',
        duration_seconds: 0, duration_availability: 'current_local',
      },
    } })
    renderView('1', 'dQw4w9WgXcQ')
    expect(await screen.findByText('0 seconds')).toBeInTheDocument()
    expect(screen.getByText('Video title unavailable')).toBeInTheDocument()
    expect(screen.getByText('Observed at 2026-11-07T10:15:30Z')).toBeInTheDocument()
  })

  it('retries only local context while preserving run evidence and identity', async () => {
    mockGetDecisionDetail.mockResolvedValueOnce({ ...baseDetail, video: { ...baseDetail.video, metadata_context_status: 'context_read_failed', current_local_metadata: null } })
      .mockResolvedValueOnce({ ...baseDetail, video: { ...baseDetail.video, title: 'Untrusted old title', current_local_metadata: { ...baseDetail.video.current_local_metadata!, title: 'Recovered local title' } } })
    renderView('1', 'dQw4w9WgXcQ')
    expect(await screen.findByText('Could not load local video information. Decision evidence remains available.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry local information' }))
    expect(await screen.findByText('Recovered local title')).toBeInTheDocument()
    expect(screen.getByText('Matched')).toBeInTheDocument()
    expect(mockGetDecisionDetail).toHaveBeenNthCalledWith(2, 1, 'dQw4w9WgXcQ')
  })

  it('keeps route-scoped exclusion separate from a hypothetical accepted outcome', async () => {
    mockGetDecisionDetail.mockResolvedValue({
      ...baseDetail,
      route_decisions: [
        { ...baseDetail.route_decisions[0], decision: 'skipped', skip_reason: 'exclusion', condition_audits: [], actions: [] },
        { ...baseDetail.route_decisions[1], decision: 'matched', skip_reason: undefined },
      ],
    })
    mockGetRun.mockResolvedValue({ id: 1, run_type: 'backtest' })
    renderView('1', 'dQw4w9WgXcQ')
    expect(await screen.findByText('Hypothetical accepted')).toBeInTheDocument()
    expect(screen.getByText('Skipped — excluded from this route.')).toBeInTheDocument()
    expect(screen.getByText('Route exclusion recorded; rule unavailable. This explanation is incomplete.')).toBeInTheDocument()
    expect(screen.getByText('Compared input unavailable in this rule-reference record.')).toBeInTheDocument()
    expect(screen.queryByText('This video was excluded')).not.toBeInTheDocument()
  })

  it('shows the captured cause in the excluded route card', async () => {
    const route = baseDetail.route_decisions[0]
    mockGetDecisionDetail.mockResolvedValue({ ...baseDetail, route_decisions: [{ ...route, route_name_source: 'run_snapshot', decision: 'skipped', skip_reason: 'exclusion', condition_audits: [], actions: [], causal_exclusion: { status: 'available', config_version_id: 42, exclusion_index: 2, semantics_version: 1, exclusion_type: 'word', field: 'title', comparison: 'lowercase_substring', configured_value: 'live', configured_value_status: 'available', matched: true, compared_input_status: 'unavailable' } }] })
    renderView('1', 'dQw4w9WgXcQ')
    expect(await screen.findByText('Recorded causal exclusion: configuration 42, exclusion #3.')).toBeInTheDocument()
    expect(screen.getByText('Matched rule: Title contains configured substring “live”, ignoring case.')).toBeInTheDocument()
    expect(screen.getByText(`Route: ${route.route_name} (ID: ${route.route_id})`)).toBeInTheDocument()
  })
})
