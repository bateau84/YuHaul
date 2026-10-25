import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
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
})
