import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RunsView, RUN_POLL_INTERVAL_MS } from './RunsView'
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

  afterEach(() => {
    vi.useRealTimers()
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

  describe('persisted lifecycle, counts, and refresh', () => {
    // Deterministic async control under fake timers: the clock only moves when
    // the test says so, and `flush` drains the promise chain, so no assertion
    // depends on wall-clock auto-advance.
    async function flush() {
      await act(async () => {
        for (let i = 0; i < 25; i++) await Promise.resolve()
        await vi.advanceTimersByTimeAsync(0)
      })
    }

    function advance(ms: number) {
      return act(async () => {
        await vi.advanceTimersByTimeAsync(ms)
        for (let i = 0; i < 25; i++) await Promise.resolve()
      })
    }

    it('renders the failed status label and offers Failed in the status filter', async () => {
      vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([runFixture({ id: 1, status: 'failed' })])

      renderView()
      await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

      expect(within(screen.getByRole('table')).getByText('Failed')).toBeInTheDocument()
      const statusSelect = screen.getByLabelText('Status')
      expect(within(statusSelect).getByRole('option', { name: 'Failed' })).toBeInTheDocument()
    })

    it('distinguishes selected, evaluated, and discovered counts, keeping null selected as Unknown', async () => {
      vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([
        runFixture({ id: 1, status: 'completed', videos_selected: 12, videos_evaluated: 7, videos_discovered: 42 }),
        runFixture({ id: 2, status: 'completed', videos_selected: 0, videos_evaluated: 4, videos_discovered: 6 }),
        runFixture({ id: 3, status: 'completed', videos_selected: null, videos_discovered: 5 }),
      ])

      renderView()
      await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())

      const table = screen.getByRole('table')
      expect(within(table).getByRole('columnheader', { name: 'Selected' })).toBeInTheDocument()
      expect(within(table).getByRole('columnheader', { name: 'Evaluated' })).toBeInTheDocument()
      expect(within(table).getByRole('columnheader', { name: 'Discovered' })).toBeInTheDocument()

      const [, row1, row2, row3] = within(table).getAllByRole('row')
      // Known counts render as numbers.
      expect(within(row1).getByText('12')).toBeInTheDocument()
      expect(within(row1).getByText('7')).toBeInTheDocument()
      expect(within(row1).getByText('42')).toBeInTheDocument()
      // A captured empty selection is 0, not Unknown.
      expect(within(row2).getByText('0')).toBeInTheDocument()
      // A never-captured selection is Unknown, not 0; an absent evaluated
      // count is an em dash, not a fabricated number.
      expect(within(row3).getByText('Unknown')).toBeInTheDocument()
      expect(within(row3).getByText('—')).toBeInTheDocument()
      expect(within(row3).getByText('5')).toBeInTheDocument()
    })

    it('polls a running run and adopts the stored terminal status, then stops polling', async () => {
      vi.useFakeTimers()
      const listSpy = vi
        .spyOn(mock, 'listRuns')
        .mockResolvedValueOnce([runFixture({ id: 1, status: 'running' })])
        .mockResolvedValueOnce([runFixture({ id: 1, status: 'failed' })])

      renderView()
      await flush()
      const table = () => screen.getByRole('table')
      expect(within(table()).getByText('Running')).toBeInTheDocument()
      expect(within(table()).queryByText('Failed')).not.toBeInTheDocument()

      await advance(RUN_POLL_INTERVAL_MS)

      expect(within(table()).getByText('Failed')).toBeInTheDocument()
      expect(within(table()).queryByText('Running')).not.toBeInTheDocument()
      expect(listSpy).toHaveBeenCalledTimes(2)

      // Terminal: no further refreshes are scheduled.
      await advance(RUN_POLL_INTERVAL_MS * 2)
      expect(listSpy).toHaveBeenCalledTimes(2)
    })

    it('keeps the last loaded list and offers a visible retry when a background refresh fails', async () => {
      vi.useFakeTimers()
      const running = runFixture({ id: 1, status: 'running' })
      vi.spyOn(mock, 'listRuns')
        .mockResolvedValueOnce([running])
        .mockRejectedValueOnce(new Error('network down'))
        .mockResolvedValueOnce([running])

      renderView()
      await flush()
      expect(screen.getByRole('table')).toBeInTheDocument()

      await advance(RUN_POLL_INTERVAL_MS)

      const alert = screen.getByRole('alert')
      expect(alert).toHaveTextContent('Could not refresh run status. The list below may be out of date.')
      // The stale-but-truthful list stays visible instead of being cleared or
      // replaced by an empty state.
      expect(within(screen.getByRole('table')).getByText('Running')).toBeInTheDocument()

      fireEvent.click(within(alert).getByRole('button', { name: 'Retry' }))
      await flush()
      expect(screen.queryByRole('alert')).not.toBeInTheDocument()
      expect(within(screen.getByRole('table')).getByText('Running')).toBeInTheDocument()
    })
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

    describe('stopping and stopped runs', () => {
      const stopRuns = (): Run[] => [
        runFixture({ id: 1, status: 'completed', videos_discovered: 11 }),
        runFixture({ id: 2, status: 'failed', videos_discovered: 22 }),
        runFixture({ id: 3, status: 'running', videos_discovered: 33 }),
        runFixture({ id: 4, status: 'running', stop_requested_at: '2026-09-08T06:05:00Z', videos_discovered: 44 }),
        runFixture({ id: 5, status: 'stopped', stop_requested_at: '2026-09-08T06:05:00Z', videos_discovered: 55 }),
      ]

      const visible = () =>
        ['11', '22', '33', '44', '55'].filter((n) => screen.queryByText(n) !== null)

      it('shows neutral Stopping and Stopped by user badges with an icon and text', async () => {
        await renderWithRuns(stopRuns())

        const rows = within(screen.getByRole('table')).getAllByRole('row')
        const stopping = within(rows[4]).getByText('Stopping')
        const stopped = within(rows[5]).getByText('Stopped by user')
        for (const badge of [stopping, stopped]) {
          expect(badge).toHaveClass('badge', 'badge--not-evaluated')
          expect(badge).not.toHaveClass('badge--failed')
          expect(badge.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
        }
        // A plain running run is not marked as stopping.
        expect(within(rows[3]).getByText('Running')).toBeInTheDocument()
        expect(within(rows[3]).queryByText('Stopping')).not.toBeInTheDocument()
      })

      it('offers both statuses in the status filter', async () => {
        await renderWithRuns(stopRuns())

        const select = screen.getByLabelText('Status')
        expect(within(select).getByRole('option', { name: 'Stopping' })).toBeInTheDocument()
        expect(within(select).getByRole('option', { name: 'Stopped by user' })).toBeInTheDocument()
      })

      it('Stopping filter shows only stopping runs', async () => {
        await renderWithRuns(stopRuns())
        fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'stopping' } })
        await waitFor(() => expect(visible()).toEqual(['44']))
      })

      it('Running filter also shows a stopping run, because it is still running', async () => {
        await renderWithRuns(stopRuns())
        fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'running' } })
        await waitFor(() => expect(visible()).toEqual(['33', '44']))
      })

      it('Stopped by user filter shows only stopped runs', async () => {
        await renderWithRuns(stopRuns())
        fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'stopped' } })
        await waitFor(() => expect(visible()).toEqual(['55']))
      })

      it('counts a stopped run as neither failed nor completed', async () => {
        await renderWithRuns(stopRuns())

        fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'failed' } })
        await waitFor(() => expect(visible()).toEqual(['22']))

        fireEvent.change(screen.getByLabelText('Status'), { target: { value: 'completed' } })
        await waitFor(() => expect(visible()).toEqual(['11']))
      })
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
