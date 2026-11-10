import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { Overview } from './Overview'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'

function renderWithToast(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <ToastProvider>{ui}</ToastProvider>
    </MemoryRouter>
  )
}

describe('Overview', () => {
  afterEach(() => vi.restoreAllMocks())

  it('shows a known running run, its phase, and disables duplicate Run Now', async () => {
    const base = await mock.getOverview()
    vi.spyOn(mock, 'getOverview').mockResolvedValueOnce({
      ...base,
      last_run: { ...base.last_run!, status: 'running', current_phase: 'discovery' },
    })

    renderWithToast(<Overview />)
    const latest = await screen.findByRole('region', { name: 'Latest run' })
    expect(within(latest).getByRole('status')).toHaveTextContent('Run 1 — Running — Current phase: Discovering videos')
    expect(within(latest).getByRole('link', { name: 'View run' })).toHaveAttribute('href', '/runs/1')
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeDisabled()
    expect(screen.getByText(/YouTube API quota used today—not run progress or playlist additions/)).toBeInTheDocument()
  })

  it('does not retry Run Now when the server accepted a run but its response is lost', async () => {
    const baseFetch = globalThis.fetch
    const showModal = HTMLDialogElement.prototype.showModal
    const closeDialog = HTMLDialogElement.prototype.close
    HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
    HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
    let startRequests = 0
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
      if (url === '/api/runs' && init?.method === 'POST') {
        startRequests++
        await baseFetch(input, init) // The test API creates and acknowledges the run.
        throw new TypeError('Failed to fetch') // Simulate losing its response in transit.
      }
      return baseFetch(input, init)
    })

    render(
      <ToastProvider>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/runs" element={<p>Runs list</p>} />
        </Routes>
      </MemoryRouter>,
      </ToastProvider>,
    )
    fireEvent.click(await screen.findByRole('button', { name: /Run Now/i }))
    fireEvent.click(screen.getByRole('button', { name: 'Confirm' }))

    expect(await screen.findByText(/Could not confirm whether a run started or resumed/)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View runs' })).toHaveAttribute('href', '/runs')
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeDisabled()
    expect(startRequests).toBe(1)
    const started = await mock.getOverview()
    if (started.last_run) started.last_run.status = 'completed'
    HTMLDialogElement.prototype.showModal = showModal
    HTMLDialogElement.prototype.close = closeDialog
  })

  it('renders empty state when last_run is null', async () => {
    vi.spyOn(mock, 'getOverview').mockResolvedValueOnce({
      last_run: null,
      runs_today: 0,
      quota: {
        daily_limit: 10000,
        consumed_today: 0,
        remaining: 10000,
        reset_at: '2026-09-09T00:00:00Z',
      },
      error_count: 0,
      deferred_count: 0,
    })

    renderWithToast(<Overview />)
    await waitFor(() => expect(screen.getByText('No runs yet.')).toBeInTheDocument())

    expect(screen.getByText('Discovered')).toBeInTheDocument()
    expect(screen.getAllByText('0')[0]).toBeInTheDocument()
  })

  it('shows an accessible fetch-error state when the overview fetch fails', async () => {
    vi.spyOn(mock, 'getOverview').mockRejectedValueOnce(new Error('Network error'))

    renderWithToast(<Overview />)

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent('Failed to load overview. Check your connection and try again.')
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    await waitFor(() => expect(retry).toHaveFocus())

    // The toolbar stays visible but disabled until a retry succeeds.
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeDisabled()
    expect(screen.getByRole('button', { name: /Dry Run/i })).toBeDisabled()

    // A failed fetch never renders the dashboard empty state.
    expect(screen.queryByText(/No runs yet/)).not.toBeInTheDocument()
  })

  it('recovers to the dashboard after a successful retry', async () => {
    vi.spyOn(mock, 'getOverview').mockRejectedValueOnce(new Error('Network error'))

    renderWithToast(<Overview />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText('Quota today')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeEnabled()
  })

  it('stays in the fetch-error state and re-enables retry when retry also fails', async () => {
    vi.spyOn(mock, 'getOverview')
      .mockRejectedValueOnce(new Error('Network error'))
      .mockRejectedValueOnce(new Error('still down'))

    renderWithToast(<Overview />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/Retry failed. Please try again./i))
    expect(screen.getByText('Failed to load overview. Check your connection and try again.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled())
    expect(screen.queryByText(/No runs yet/)).not.toBeInTheDocument()
  })
})
