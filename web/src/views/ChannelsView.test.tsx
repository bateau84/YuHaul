import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { ChannelsView } from './ChannelsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { ChannelDetail } from '../types'

describe('ChannelsView', () => {
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
})
