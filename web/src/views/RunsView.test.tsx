import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RunsView } from './RunsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Run } from '../types'

const runFixture = (overrides: Partial<Run> = {}): Run => ({
  id: 1,
  started_at: '2026-09-08T06:00:00Z',
  run_type: 'scheduled',
  status: 'completed',
  videos_discovered: 42,
  videos_accepted: 3,
  videos_rejected: 23,
  videos_excluded: 4,
  videos_deferred: 12,
  videos_failed: 0,
  videos_not_yet_evaluated: 0,
  quota_cost: 89,
  ...overrides,
})

function renderView() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/runs']}>
        <Routes>
          <Route path="/runs" element={<RunsView />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>
  )
}

describe('RunsView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders run rows when runs are returned', async () => {
    vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([runFixture()])

    renderView()

    await waitFor(() => expect(screen.getByRole('link', { name: /9\/8\/2026/i })).toBeInTheDocument())
    // Scoped to the table: the run type filter also offers a "Scheduled" option.
    const table = screen.getByRole('table')
    expect(within(table).getByText('Scheduled')).toBeInTheDocument()
    expect(within(table).getByText('42')).toBeInTheDocument()
  })

  it('renders empty state when the fetch succeeds with an empty list', async () => {
    vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([])

    renderView()

    await waitFor(() => expect(screen.getByText(/No runs yet/i)).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('rejects a null runs response as an error instead of normalizing it to empty', async () => {
    vi.spyOn(mock, 'listRuns').mockResolvedValueOnce(null as unknown as Run[])

    renderView()

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/Failed to load runs/i))
    expect(screen.queryByText(/No runs yet/i)).not.toBeInTheDocument()
  })

  it('shows the fetch error state with an accessible retry button when the list fetch fails', async () => {
    vi.spyOn(mock, 'listRuns').mockRejectedValueOnce(new Error('network down'))

    renderView()

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent('Failed to load runs. Check your connection and try again.')
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    expect(retry).toBeEnabled()
    await waitFor(() => expect(retry).toHaveFocus())

    expect(screen.queryByText(/No runs yet/i)).not.toBeInTheDocument()
  })

  it('stays in the fetch error state and re-enables retry when retry also fails', async () => {
    vi.spyOn(mock, 'listRuns')
      .mockRejectedValueOnce(new Error('network down'))
      .mockRejectedValueOnce(new Error('still down'))

    renderView()

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/Retry failed. Please try again./i))
    // The error region persists (disambiguated from the error toast, which is also role=alert).
    expect(screen.getByText(/Failed to load runs\. Check your connection and try again\./)).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled())
    expect(screen.queryByText(/No runs yet/i)).not.toBeInTheDocument()
  })

  it('recovers to the populated list after a successful retry', async () => {
    vi.spyOn(mock, 'listRuns')
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce([runFixture()])

    renderView()

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    // Assert on the row link, not bare text: the run type filter renders a
    // "Scheduled" option that would otherwise satisfy this query.
    await waitFor(() => expect(screen.getByRole('link', { name: /9\/8\/2026/i })).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    // Focus moves to the first run row after a successful retry.
    await waitFor(() => expect(screen.getAllByRole('row')[1]).toHaveFocus())
  })

  it('recovers to the empty state only when a retry succeeds with an empty list', async () => {
    vi.spyOn(mock, 'listRuns')
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce([])

    renderView()

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText(/No runs yet/i)).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  describe('filters', () => {
    const filterRuns = (): Run[] => [
      runFixture({
        id: 1,
        run_type: 'scheduled',
        status: 'completed',
        started_at: '2026-09-08T06:00:00Z',
        videos_discovered: 11,
        quota_cost: 1,
      }),
      runFixture({
        id: 2,
        run_type: 'manual',
        status: 'interrupted',
        started_at: '2026-09-07T06:00:00Z',
        videos_discovered: 22,
        quota_cost: 2,
      }),
      runFixture({
        id: 3,
        run_type: 'dry_run',
        status: 'running',
        started_at: '2026-09-01T06:00:00Z',
        videos_discovered: 33,
        quota_cost: 3,
      }),
    ]

    async function renderWithRuns(runs: Run[]) {
      vi.spyOn(mock, 'listRuns').mockResolvedValueOnce(runs)
      renderView()
      await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
    }

    it('filters runs by run type', async () => {
      await renderWithRuns(filterRuns())

      fireEvent.change(screen.getByLabelText('Run type'), { target: { value: 'manual' } })

      await waitFor(() => expect(screen.getByText('22')).toBeInTheDocument())
      expect(screen.queryByText('11')).not.toBeInTheDocument()
      expect(screen.queryByText('33')).not.toBeInTheDocument()
    })

    it('filters runs by status', async () => {
      await renderWithRuns(filterRuns())

      fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'running' } })

      await waitFor(() => expect(screen.getByText('33')).toBeInTheDocument())
      expect(screen.queryByText('11')).not.toBeInTheDocument()
      expect(screen.queryByText('22')).not.toBeInTheDocument()
    })

    it('filters runs by the date range of the run start', async () => {
      await renderWithRuns(filterRuns())

      fireEvent.change(screen.getByLabelText('From date'), { target: { value: '2026-09-07' } })
      fireEvent.change(screen.getByLabelText('To date'), { target: { value: '2026-09-07' } })

      await waitFor(() => expect(screen.getByText('22')).toBeInTheDocument())
      expect(screen.queryByText('11')).not.toBeInTheDocument()
      expect(screen.queryByText('33')).not.toBeInTheDocument()
    })

    it('combines the date range, run type, and status filters', async () => {
      await renderWithRuns(filterRuns())

      fireEvent.change(screen.getByLabelText('From date'), { target: { value: '2026-09-07' } })
      fireEvent.change(screen.getByLabelText('To date'), { target: { value: '2026-09-30' } })
      fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'completed' } })

      await waitFor(() => expect(screen.getByText('11')).toBeInTheDocument())
      expect(screen.queryByText('22')).not.toBeInTheDocument()
      expect(screen.queryByText('33')).not.toBeInTheDocument()
    })

    it('shows a filtered empty state instead of the unfiltered empty state when filters exclude every run', async () => {
      await renderWithRuns(filterRuns())

      fireEvent.change(screen.getByLabelText('Run type'), { target: { value: 'replay' } })

      await waitFor(() => expect(screen.getByText('No runs match the current filters.')).toBeInTheDocument())
      expect(screen.queryByText(/No runs yet/i)).not.toBeInTheDocument()
      expect(screen.queryByRole('table')).not.toBeInTheDocument()
    })

    it('keeps the unfiltered empty state when the fetch succeeds with an empty list', async () => {
      vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([])
      renderView()

      expect(await screen.findByText(/No runs yet/i)).toBeInTheDocument()
      expect(screen.queryByText(/No runs match the current filters/i)).not.toBeInTheDocument()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    })

    it('disables the filter controls while the list is in the fetch-error state', async () => {
      vi.spyOn(mock, 'listRuns').mockRejectedValueOnce(new Error('network down'))

      renderView()

      await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/Failed to load runs/i))
      expect(screen.getByLabelText('From date')).toBeDisabled()
      expect(screen.getByLabelText('To date')).toBeDisabled()
      expect(screen.getByLabelText('Run type')).toBeDisabled()
      expect(screen.getByLabelText('Status')).toBeDisabled()
    })
  })
})
