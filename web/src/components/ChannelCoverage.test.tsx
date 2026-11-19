import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen } from '@testing-library/react'
import { ChannelCoverage } from './ChannelCoverage'
import * as sourceApi from '../api/source-failures'
import type { Run, SourceFailure, SourceFailurePage } from '../types'

const CH_B = `UC${'B'.repeat(22)}`
const NONE_SCANNED = 'diag:v1 phase=discovery operation=discover code=no_source_scanned'

const baseRun: Run = {
  id: 54,
  started_at: '2026-11-20T06:00:00Z',
  run_type: 'manual',
  status: 'completed',
  videos_discovered: 2,
  videos_accepted: 0,
  videos_rejected: 0,
  videos_excluded: 0,
  videos_deferred: 0,
  videos_failed: 0,
  videos_not_yet_evaluated: 0,
  quota_cost: 10,
}

function failure(over: Partial<SourceFailure> = {}): SourceFailure {
  return {
    id: 5,
    run_id: 54,
    channel_id: CH_B,
    channel_name: 'Channel B',
    occurred_at: '2026-11-20T12:00:00Z',
    category: 'permanent',
    operation: 'playlist_items_list',
    http_status: 404,
    reason: 'playlistNotFound',
    ...over,
  }
}
const page = (items: SourceFailure[], next: number | null = null): SourceFailurePage => ({ items, next_before_id: next })

afterEach(() => vi.restoreAllMocks())

describe('Run Detail: channel coverage block', () => {
  it('lists the failed channel under the partial-scan sentence on a completed run', async () => {
    vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure()]))
    render(<ChannelCoverage run={baseRun} />)

    expect(await screen.findByRole('heading', { name: 'Some channels could not be scanned. Results are incomplete.' })).toBeInTheDocument()
    expect(screen.getByText('Channels not scanned:')).toBeInTheDocument()
    expect(screen.getByText('Channel B')).toBeInTheDocument()
    expect(screen.getByText(`Channel ID: ${CH_B}`)).toBeInTheDocument()
    expect(screen.getByText(/Uploads could not be scanned\. YouTube reported that the uploads playlist was not found\./)).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    // Never claims coverage, a count or "all".
    expect(document.body.textContent).not.toMatch(/\ball\b|\bevery\b|remaining|scanned successfully/i)
  })

  it('renders nothing at all for a run with no recorded failure', async () => {
    const spy = vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([]))
    const { container } = render(<ChannelCoverage run={baseRun} />)
    await vi.waitFor(() => expect(spy).toHaveBeenCalled())
    await vi.waitFor(() => expect(screen.queryByText('Loading channel scan details…')).not.toBeInTheDocument())
    expect(container).toBeEmptyDOMElement()
  })

  it('keeps "Run failed. No channels were scanned successfully." for the no_source_scanned ending', async () => {
    vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure()]))
    render(<ChannelCoverage run={{ ...baseRun, status: 'failed', interrupt_reason: NONE_SCANNED }} />)
    expect(await screen.findByRole('heading', { name: 'Run failed. No channels were scanned successfully.' })).toBeInTheDocument()
    expect(screen.getByText('Channel B')).toBeInTheDocument()
  })

  it.each([
    ['failed for another reason', { status: 'failed' as const, interrupt_reason: 'diag:v1 phase=discovery operation=discover code=provider_error status=403 reason=quotaExceeded' }],
    ['interrupted', { status: 'interrupted' as const }],
    ['stopped', { status: 'stopped' as const }],
  ])('uses the scan-stage-not-established wording for a run that %s', async (_n, patch) => {
    vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure()]))
    render(<ChannelCoverage run={{ ...baseRun, ...patch }} />)
    expect(await screen.findByRole('heading', { name: 'This run did not complete normally, so results are incomplete. See the run status.' })).toBeInTheDocument()
    expect(screen.getByText('Channels recorded as not scanned:')).toBeInTheDocument()
    expect(screen.getByText('YuHaul has no record of which other channels were scanned.')).toBeInTheDocument()
    expect(screen.queryByText(/Some channels could not be scanned/)).not.toBeInTheDocument()
    expect(screen.queryByText(/No channels were scanned successfully/)).not.toBeInTheDocument()
  })

  it('does not show transient rows, which this design does not cover', async () => {
    vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure({ category: 'transient', reason: 'backendError', http_status: 500 })]))
    const { container } = render(<ChannelCoverage run={baseRun} />)
    await vi.waitFor(() => expect(screen.queryByText('Loading channel scan details…')).not.toBeInTheDocument())
    expect(container).toBeEmptyDOMElement()
  })

  it('shows loading, and a failed read as an alert with Reload and never as "no failures"', async () => {
    let resolveFirst!: (p: SourceFailurePage) => void
    const spy = vi
      .spyOn(sourceApi, 'getRunSourceFailures')
      .mockReturnValueOnce(new Promise((r) => { resolveFirst = r }))
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValue(page([failure()]))
    const { rerender } = render(<ChannelCoverage run={baseRun} />)
    expect(screen.getByText('Loading channel scan details…')).toBeInTheDocument()
    resolveFirst(page([]))
    await vi.waitFor(() => expect(screen.queryByText('Loading channel scan details…')).not.toBeInTheDocument())

    // A later read (the run ended) fails: the block says so and offers Reload.
    rerender(<ChannelCoverage run={{ ...baseRun, status: 'interrupted' }} />)
    expect(await screen.findByText('Could not load channel scan details. Nothing was changed.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Reload channel scan details' }))
    expect(await screen.findByText('Channel B')).toBeInTheDocument()
    expect(spy).toHaveBeenCalledTimes(3)
  })

  it('reads more channels by cursor', async () => {
    const spy = vi
      .spyOn(sourceApi, 'getRunSourceFailures')
      .mockResolvedValueOnce(page([failure({ id: 9 })], 9))
      .mockResolvedValueOnce(page([failure({ id: 4, channel_name: 'Channel C' })]))
    render(<ChannelCoverage run={baseRun} />)
    fireEvent.click(await screen.findByRole('button', { name: 'Show more channels' }))
    expect(await screen.findByText('Channel C')).toBeInTheDocument()
    expect(spy).toHaveBeenLastCalledWith(54, { beforeId: 9 })
    expect(screen.queryByRole('button', { name: 'Show more channels' })).not.toBeInTheDocument()
  })
})
