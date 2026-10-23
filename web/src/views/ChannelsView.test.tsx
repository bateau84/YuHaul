import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { ChannelsView } from './ChannelsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import * as api from '../api/client'
import type { ChannelDetail } from '../types'

describe('ChannelsView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  function renderView(initialEntry = '/analytics/channels') {
    return render(
      <MemoryRouter initialEntries={[initialEntry]}>
        <ToastProvider>
          <Routes>
            <Route path="/analytics/channels" element={<ChannelsView />} />
            <Route path="/analytics/channels/:channelId" element={<ChannelsView />} />
          </Routes>
        </ToastProvider>
      </MemoryRouter>
    )
  }

  it('renders the channel list with advisory badges', async () => {
    renderView()
    await waitFor(() => expect(screen.getByText('Jazz Club')).toBeInTheDocument())

    expect(screen.getByText('Focus Sounds')).toBeInTheDocument()
    expect(screen.getByText('Low usefulness')).toBeInTheDocument()
    expect(screen.getByText('High duplicate rate')).toBeInTheDocument()
  })

  it('renders channel detail with provenance breakdowns', async () => {
    renderView('/analytics/channels/UCdemo1')
    await waitFor(() => expect(screen.getByText('Jazz Club')).toBeInTheDocument())

    expect(screen.getByText('Discovery & Relevance Credit')).toBeInTheDocument()
    expect(screen.getByText('Retention Signal Provenance')).toBeInTheDocument()
    expect(screen.getByText('First discovered')).toBeInTheDocument()
    expect(screen.getByText('User-added')).toBeInTheDocument()
    expect(screen.getByText(/not counted as retention/)).toBeInTheDocument()
  })

  it('renders channel detail without crashing on sparse server response', async () => {
    const sparse: ChannelDetail = {
      channel_id: 'UCsparse',
      channel_name: 'Sparse Channel',
      total_discovered: 10,
      total_routed: 4,
      total_retained: 2,
      usefulness_rate: 0.2,
      yield: 0.4,
    } as unknown as ChannelDetail
    vi.spyOn(mock, 'getChannel').mockResolvedValueOnce(sparse)
    renderView('/analytics/channels/UCsparse')

    await waitFor(() => expect(screen.getByText('Sparse Channel')).toBeInTheDocument())
    expect(screen.getByText('Discovery & Relevance Credit')).toBeInTheDocument()
    expect(screen.getByText('Retention Signal Provenance')).toBeInTheDocument()
  })

  // The fixture below is the exact JSON emitted by GET /api/analytics/channels/UC1
  // on a real server with a seeded advisory + recent video (asserted by
  // internal/stats/handlers_test.go:TestHandler_GetChannel_RealResponseShape).
  // It proves the component renders the server's advisory_type/channel_id
  // vocabulary without the "Element type is invalid" crash the reviewer flagged.
  it('renders an advisory from the real server response shape', async () => {
    const realServerResponse: ChannelDetail = {
      channel_id: 'UC1',
      channel_name: 'Channel One',
      total_discovered: 100,
      total_routed: 40,
      total_retained: 5,
      yield: 0.4,
      usefulness_rate: 0.05,
      duplicate_rate: 0.01,
      duplicate_provenance: { discovery_credit: 0, relevance_credit: 1 },
      retention_signals: { user_driven: 0, yuhaul_routed: 0 },
      recent_videos: [
        {
          id: 1,
          video_id: 'v1',
          channel_id: 'UC1',
          title: 'Test Title',
          processing_state: 'completed',
          final_outcome: 'accepted',
          published_at: '2026-09-09T19:39:57.811402Z',
          discovered_at: '2026-09-10T19:39:57.811402Z',
          completed_at: '2026-09-10T19:39:57.811402Z',
        },
      ],
      recent_video_provenance: [],
      matched_routes: ['Music'],
      advisories: [
        {
          id: 1,
          channel_id: 'UC1',
          channel_name: 'Channel One',
          advisory_type: 'low_usefulness',
          status: 'active',
          generated_at: '2026-09-10T19:39:57.811402Z',
          evidence_snapshot: { window_days: 90, discovered: 100, retained: 5 },
        },
      ],
    }
    vi.spyOn(api, 'getChannel').mockResolvedValue(realServerResponse)
    renderView('/analytics/channels/UC1')

    await waitFor(() => expect(screen.getByText('Channel One')).toBeInTheDocument())

    // Advisory region renders the real advisory (label lookup from advisory_type).
    expect(screen.getByText('Low usefulness')).toBeInTheDocument()
    // Evidence line derived from evidence_snapshot.
    expect(screen.getByText(/5\.0% usefulness \(5 retained of 100 discovered\) over 90 days\./)).toBeInTheDocument()
    // Action buttons render (dismiss/ignore/act).
    expect(screen.getByRole('button', { name: /Dismiss/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Ignore 30 days/i })).toBeInTheDocument()
    // Recent video region renders title and outcome.
    expect(screen.getByText('Test Title')).toBeInTheDocument()
    expect(screen.getByText('Accepted')).toBeInTheDocument()
  })

  it('dismisses an advisory from the detail view', async () => {
    const dismissSpy = vi.spyOn(mock, 'dismissAdvisory')
    renderView('/analytics/channels/UCdemo1')
    await waitFor(() => expect(screen.getByText('Jazz Club')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Dismiss/i }))
    await waitFor(() => expect(dismissSpy).toHaveBeenCalledWith(1))
  })
})
