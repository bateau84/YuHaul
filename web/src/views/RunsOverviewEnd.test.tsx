import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { RunsView } from './RunsView'
import { Overview } from './Overview'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Run } from '../types'

const END = 'diag:v1 phase=unknown operation=end_legacy_run code=ended_by_user'

const base: Run = {
  id: 14, started_at: '2026-10-20T06:00:00Z', run_type: 'manual', status: 'running',
  videos_discovered: 19615, videos_accepted: 0, videos_rejected: 0, videos_excluded: 0, videos_deferred: 0,
  videos_failed: 0, videos_not_yet_evaluated: 19615, quota_cost: 0, current_phase: 'discovery',
  endable: true, executing: false,
}

afterEach(() => vi.restoreAllMocks())

describe('Run list — Stop follows executing, not the saved status', () => {
  function renderRuns() {
    return render(<MemoryRouter><ToastProvider><RunsView /></ToastProvider></MemoryRouter>)
  }

  it('offers Stop only on the run the server is executing, never on an endable or non-executing run', async () => {
    vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([
      { ...base, id: 41, run_type: 'scheduled', endable: false, executing: true, started_at: '2026-11-10T06:00:00Z' },
      { ...base, id: 14 },
      { ...base, id: 15, endable: false, executing: false },
      { ...base, id: 16, endable: undefined, executing: undefined },
    ])
    renderRuns()
    expect(await screen.findByRole('button', { name: 'Stop run 41' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^Stop run/ })).toHaveLength(1)
  })

  it('shows run 14 without Stop while another production run is active', async () => {
    vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([
      { ...base, id: 41, run_type: 'scheduled', endable: false, executing: true, started_at: '2026-11-10T06:00:00Z' },
      { ...base, id: 14, endable: false, executing: false },
    ])
    renderRuns()
    await screen.findByRole('button', { name: 'Stop run 41' })
    expect(screen.queryByRole('button', { name: 'Stop run 14' })).toBeNull()
  })

  it('shows an ended run as Failed with "Ended by user (legacy run)", not as a discard', async () => {
    vi.spyOn(mock, 'listRuns').mockResolvedValueOnce([
      { ...base, id: 14, status: 'failed', endable: false, interrupt_reason: END },
      { ...base, id: 30, status: 'failed', endable: false, interrupt_reason: 'diag:v1 phase=unknown operation=discard_run code=snapshot_unavailable' },
    ])
    renderRuns()
    await screen.findAllByText('Failed')
    expect(screen.getAllByText('Ended by user (legacy run)')).toHaveLength(1)
    const rows = screen.getAllByRole('row')
    expect(within(rows[1]).getByText('Ended by user (legacy run)')).toBeInTheDocument()
  })

  it('does not poll forever for an endable run', async () => {
    vi.useFakeTimers()
    try {
      const spy = vi.spyOn(mock, 'listRuns').mockResolvedValue([{ ...base }])
      renderRuns()
      await vi.advanceTimersByTimeAsync(20000)
      expect(spy).toHaveBeenCalledTimes(1)
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('Overview — an endable run is not in progress', () => {
  function renderOverview() {
    return render(<MemoryRouter><ToastProvider><Overview /></ToastProvider></MemoryRouter>)
  }

  it('does not hold back Run Now, says nothing executes it, and offers no Stop', async () => {
    const ov = await mock.getOverview()
    vi.spyOn(mock, 'getOverview').mockResolvedValue({ ...ov, last_run: { ...base } })
    renderOverview()
    const latest = await screen.findByRole('region', { name: 'Latest run' })
    expect(latest).toHaveTextContent('Run 14 — manual — Marked as running — nothing is executing it')
    expect(latest).not.toHaveTextContent('Run in progress')
    expect(latest).not.toHaveTextContent('Current phase')
    expect(within(latest).queryByRole('button', { name: /^Stop run/ })).toBeNull()
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeEnabled()
  })

  it('keeps Run Now disabled and offers Stop for a run the server is executing', async () => {
    const ov = await mock.getOverview()
    vi.spyOn(mock, 'getOverview').mockResolvedValue({ ...ov, last_run: { ...base, id: 41, endable: false, executing: true } })
    renderOverview()
    expect(await screen.findByRole('button', { name: 'Stop run 41' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Run Now/i })).toBeDisabled()
  })

  it('shows no Stop for a running run that is not executing and not endable (run 14 beside an active run)', async () => {
    const ov = await mock.getOverview()
    vi.spyOn(mock, 'getOverview').mockResolvedValue({ ...ov, last_run: { ...base, endable: false, executing: false } })
    renderOverview()
    await waitFor(() => expect(screen.getByRole('region', { name: 'Latest run' })).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /^Stop run/ })).toBeNull()
  })
})
