import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { ErrorsView } from './ErrorsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <ToastProvider>{ui}</ToastProvider>
    </MemoryRouter>
  )
}

describe('ErrorsView', () => {
  it('renders errors and deferred queues with retry/dismiss actions', async () => {
    renderWithProviders(<ErrorsView />)

    await waitFor(() => expect(screen.getByRole('heading', { name: /Errors & Deferred/i })).toBeInTheDocument())

    // Default errors tab should show the mock error.
    await waitFor(() => expect(screen.getByText('Ambient Study Session')).toBeInTheDocument())
    expect(screen.getByText('OAuth token expired during playlist insertion.')).toBeInTheDocument()

    const errorRow = screen.getByText('Ambient Study Session').closest('tr')
    expect(errorRow).not.toBeNull()
    expect(within(errorRow!).getByRole('button', { name: /Retry/i })).toBeInTheDocument()
    expect(within(errorRow!).getByRole('button', { name: /Remove/i })).toBeInTheDocument()

    // Switch to deferred tab.
    fireEvent.click(screen.getByRole('tab', { name: /Deferred/i }))
    await waitFor(() => expect(screen.getByText('Pop Hit 2026')).toBeInTheDocument())
    expect(screen.getByText('quota_exhausted')).toBeInTheDocument()

    const deferredRow = screen.getByText('Pop Hit 2026').closest('tr')
    expect(deferredRow).not.toBeNull()
    expect(within(deferredRow!).getByRole('button', { name: /Retry now/i })).toBeInTheDocument()
    expect(within(deferredRow!).getByRole('button', { name: /Discard/i })).toBeInTheDocument()
  })

  it('dismisses an error and removes it from the list', async () => {
    renderWithProviders(<ErrorsView />)

    await waitFor(() => expect(screen.getByText('Ambient Study Session')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Remove/i }))

    await waitFor(() => expect(screen.queryByText('Ambient Study Session')).not.toBeInTheDocument())
    expect(screen.getByText(/No errors/i)).toBeInTheDocument()
  })

  it('shows error state when queue fetch fails', async () => {
    vi.spyOn(mock, 'listErrors').mockRejectedValueOnce(new Error('Queue unavailable'))

    renderWithProviders(<ErrorsView />)

    await waitFor(() => expect(screen.getByText(/Queue unavailable/i)).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()
  })
})
