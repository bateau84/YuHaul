import { describe, it, expect } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { DecisionDetailView } from './DecisionDetailView'
import { ToastProvider } from '../components/ToastProvider'

describe('DecisionDetailView', () => {
  function renderView(runId: string, videoId: string) {
    return render(
      <MemoryRouter initialEntries={[`/runs/${runId}/video/${videoId}`]}>
        <ToastProvider>
          <Routes>
            <Route path="/runs/:id/video/:videoId" element={<DecisionDetailView />} />
          </Routes>
        </ToastProvider>
      </MemoryRouter>
    )
  }

  it('renders the three-layer evidence model for an accepted video', async () => {
    renderView('1', '1001')
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
    renderView('1', '1001')
    await waitFor(() => expect(screen.getByText('Late Night Jazz Mix')).toBeInTheDocument())

    expect(screen.getByText('Skipped')).toBeInTheDocument()
    expect(screen.getByText(/A prior final route already matched this video/)).toBeInTheDocument()
  })

  it('shows condition mismatch for a rejected video', async () => {
    renderView('1', '1002')
    await waitFor(() => expect(screen.getByText('Ambient Study Session')).toBeInTheDocument())

    expect(screen.getByText('Did not match')).toBeInTheDocument()
    expect(screen.getByText(/title does not contain/)).toBeInTheDocument()
    expect(screen.getByText(/This route is currently disabled/)).toBeInTheDocument()
  })
})
