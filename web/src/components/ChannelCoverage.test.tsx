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
    render(<ChannelCoverage run={{ ...baseRun, discovery_scan: 'completed' }} />)

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
    render(<ChannelCoverage run={{ ...baseRun, status: 'failed', interrupt_reason: NONE_SCANNED, discovery_scan: 'no_source_scanned' }} />)
    expect(await screen.findByRole('heading', { name: 'Run failed. No channels were scanned successfully.' })).toBeInTheDocument()
    expect(screen.getByText('Channel B')).toBeInTheDocument()
  })

  describe('scan cut short (saved discovery_scan = cut_short)', () => {
    it.each(['completed', 'failed', 'interrupted', 'stopped'] as const)('uses the cut-short wording for a %s run', async (status) => {
      vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure()]))
      render(<ChannelCoverage run={{ ...baseRun, status, discovery_scan: 'cut_short' }} />)
      expect(await screen.findByRole('heading', { name: 'Channel scanning stopped before it finished. Results are incomplete.' })).toBeInTheDocument()
      expect(screen.getByText('See the run status for the cause.')).toBeInTheDocument()
      expect(screen.getByText('Channels recorded as not scanned:')).toBeInTheDocument()
      expect(screen.getByText('Channel B')).toBeInTheDocument()
      expect(screen.getByText('YuHaul has no record of which other channels were scanned.')).toBeInTheDocument()
      expect(screen.queryByText(/Some channels could not be scanned/)).not.toBeInTheDocument()
      expect(screen.queryByText(/No channels were scanned successfully/)).not.toBeInTheDocument()
      const completedSentence = screen.queryByText("Completed is the run's saved status; it does not mean every channel was scanned.")
      if (status === 'completed') expect(completedSentence).toBeInTheDocument()
      else expect(completedSentence).not.toBeInTheDocument()
    })
  })

  it.each([
    ['a normally finished scan', { status: 'completed' as const, discovery_scan: 'completed' as const }],
    ['a run still scanning', { status: 'running' as const, discovery_scan: 'in_progress' as const }],
    ['no_source_scanned on a run that did not fail (a Stop won)', { status: 'stopped' as const, discovery_scan: 'no_source_scanned' as const }],
  ])('keeps "Some channels could not be scanned." for %s', async (_n, patch) => {
    vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure()]))
    render(<ChannelCoverage run={{ ...baseRun, ...patch }} />)
    expect(await screen.findByRole('heading', { name: 'Some channels could not be scanned. Results are incomplete.' })).toBeInTheDocument()
    expect(screen.getByText('Channels not scanned:')).toBeInTheDocument()
    expect(screen.queryByText(/stopped before it finished/)).not.toBeInTheDocument()
    expect(screen.queryByText(/No channels were scanned successfully/)).not.toBeInTheDocument()
  })

  describe('unknown scan outcome with recorded rows (an integrity anomaly)', () => {
    it.each([['failed' as const], ['interrupted' as const], ['stopped' as const]])('says the run did not complete normally for %s', async (status) => {
      vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure()]))
      render(<ChannelCoverage run={{ ...baseRun, status, discovery_scan: null }} />)
      expect(await screen.findByRole('heading', { name: 'This run did not complete normally, so results are incomplete. See the run status.' })).toBeInTheDocument()
      expect(screen.getByText('Channels recorded as not scanned:')).toBeInTheDocument()
    })

    it.each([[null], [undefined]])('shows only the list for a completed run (%s)', async (scan) => {
      vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([failure()]))
      render(<ChannelCoverage run={{ ...baseRun, discovery_scan: scan }} />)
      expect(await screen.findByText('Channel B')).toBeInTheDocument()
      expect(screen.getByText('Channels recorded as not scanned:')).toBeInTheDocument()
      expect(screen.queryByRole('heading')).not.toBeInTheDocument()
      expect(screen.queryByText(/incomplete|stopped|Some channels/)).not.toBeInTheDocument()
    })
  })

  it.each(['completed', 'cut_short', 'no_source_scanned', 'in_progress', null] as const)('shows no block for a zero-row run whose scan is %s', async (scan) => {
    const spy = vi.spyOn(sourceApi, 'getRunSourceFailures').mockResolvedValue(page([]))
    const { container } = render(<ChannelCoverage run={{ ...baseRun, discovery_scan: scan }} />)
    await vi.waitFor(() => expect(spy).toHaveBeenCalled())
    await vi.waitFor(() => expect(screen.queryByText('Loading channel scan details…')).not.toBeInTheDocument())
    expect(container).toBeEmptyDOMElement()
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
