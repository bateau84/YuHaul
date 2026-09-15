import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { DecisionDetailView } from './DecisionDetailView'
import { ToastProvider } from '../components/ToastProvider'
import type { DecisionDetail } from '../types'

const mockGetDecisionDetail = vi.fn()

vi.mock('../api/client', () => ({
  getDecisionDetail: (...args: [number, string]) => mockGetDecisionDetail(...args),
}))

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
      id: 1,
      route_id: 1,
      route_name: 'Music Discovery',
      evaluation_order: 1,
      decision: 'matched',
      condition_audits: [
        { id: 0, condition_index: 0, field: 'title', operator: 'contains', expected_value: 'music', actual_value: 'Late Night Jazz Mix', matched: true },
        { id: 1, condition_index: 1, field: 'duration_seconds', operator: 'gt', expected_value: '180', actual_value: '184', matched: true },
      ],
      actions: [{ id: 0, action_type: 'playlist_insert', target_playlist_id: 'PLmusic', outcome: 'succeeded' }],
    },
    {
      id: 2,
      route_id: 2,
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
    expect(screen.getByText('Actions executed')).toBeInTheDocument()

    expect(screen.getByText('Layer 3 — Action outcomes')).toBeInTheDocument()
    expect(screen.getAllByText('Succeeded').length).toBeGreaterThanOrEqual(1)
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
          id: 1,
          route_id: 1,
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
          id: 2,
          route_id: 2,
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
})
