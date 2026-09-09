import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { SettingsView } from './SettingsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'

function renderWithToast(ui: React.ReactElement) {
  return render(<ToastProvider>{ui}</ToastProvider>)
}

describe('SettingsView', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/settings')
  })

  it('shows connect button when account is not connected', async () => {
    vi.spyOn(mock, 'getSettings').mockResolvedValueOnce({
      schedule: '0 6 * * *',
      quota_limit: 10000,
      duplicate_threshold: 0.5,
      duplicate_behavior: 'skip',
      account_connected: false,
      account_email: undefined,
    })

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(screen.getByRole('button', { name: /Connect YouTube account/i })).toBeInTheDocument())
  })

  it('shows success toast and refreshes status when auth=connected query param is present', async () => {
    window.history.replaceState({}, '', '/settings?auth=connected')

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/YouTube account connected/i))

    // Query parameter should be stripped.
    expect(window.location.search).toBe('')
  })

  it('shows error state when settings fetch fails', async () => {
    vi.spyOn(mock, 'getSettings').mockRejectedValueOnce(new Error('Server error'))

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(screen.getByText(/Server error/i)).toBeInTheDocument())

    expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()
  })

  it('initiates OAuth flow when connect button is clicked', async () => {
    vi.spyOn(mock, 'getSettings').mockResolvedValueOnce({
      schedule: '0 6 * * *',
      quota_limit: 10000,
      duplicate_threshold: 0.5,
      duplicate_behavior: 'skip',
      account_connected: false,
      account_email: undefined,
    })

    const assignMock = vi.fn()
    const originalLocation = window.location
    // @ts-expect-error jsdom location is not configurable; replace for the test.
    delete window.location
    window.location = { ...originalLocation, assign: assignMock } as unknown as Location

    renderWithToast(<SettingsView />)
    await waitFor(() => expect(screen.getByRole('button', { name: /Connect YouTube account/i })).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Connect YouTube account/i }))
    await waitFor(() => expect(assignMock).toHaveBeenCalledWith('https://accounts.google.com/oauth/mock'))

    window.location = originalLocation
  })
})
