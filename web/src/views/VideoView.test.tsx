import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { VideoView } from './VideoView'
import type { VideoRunEntry, VideoView as VideoViewData } from '../types'

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const base = (over: Partial<VideoViewData> = {}): VideoViewData => ({
  video_id: 'dQw4w9WgXcQ',
  record: 'known',
  title: { text: 'Lofi Beats to Study To (Live Remix)', availability: 'current_local' },
  channel: { name: 'ChillHop', name_availability: 'current_local', channel_id: 'UC1' },
  latest_outcome: { state: 'committed', outcome: 'rejected', run_id: 58, committed_at: '2026-11-10T10:00:00Z' },
  queue_state: { state: 'none' },
  runs: [],
  ...over,
})

const run = (id: number, over: Partial<VideoRunEntry> = {}): VideoRunEntry => ({
  run_id: id,
  run_type: 'scheduled',
  mode: 'production',
  run_status: 'completed',
  outcome: 'rejected',
  decision_detail_path: `/runs/${id}/video/dQw4w9WgXcQ`,
  ...over,
})

function renderAt(url: string, state?: unknown) {
  return render(
    <MemoryRouter initialEntries={[{ pathname: url, state }]}>
      <Routes>
        <Route path="/videos/:videoId" element={<VideoView />} />
      </Routes>
    </MemoryRouter>
  )
}

let fetchMock: ReturnType<typeof vi.spyOn>
beforeEach(() => {
  fetchMock = vi.spyOn(globalThis, 'fetch')
})
afterEach(() => vi.restoreAllMocks())

describe('VideoView', () => {
  it('shows title, channel, outcome, queue state and runs with simulation labels, and focuses the H1', async () => {
    fetchMock.mockResolvedValue(
      json(
        base({
          runs: [
            run(61, { run_type: 'dry_run', mode: 'simulation', outcome: 'accepted', decision_detail_path: '/runs/61/video/dQw4w9WgXcQ' }),
            run(58),
            run(40, { run_type: 'manual' }),
          ],
        })
      )
    )
    renderAt('/videos/dQw4w9WgXcQ', { from: '/videos?q=lofi&page=2' })
    const h1 = await screen.findByRole('heading', { level: 1, name: 'Lofi Beats to Study To (Live Remix)' })
    await waitFor(() => expect(h1).toHaveFocus())
    expect(fetchMock.mock.calls[0][0]).toBe('/api/videos/dQw4w9WgXcQ')
    expect(screen.getByRole('link', { name: 'Back to search' })).toHaveAttribute('href', '/videos?q=lofi&page=2')
    expect(screen.getByText('Video ID: dQw4w9WgXcQ')).toBeInTheDocument()
    expect(screen.getByText("Shown from YuHaul's own records. YouTube was not asked.")).toBeInTheDocument()
    expect(screen.getByText('Current/local information — not necessarily the information evaluated by any run.')).toBeInTheDocument()
    expect(screen.getByText(/Source channel: ChillHop/)).toBeInTheDocument()

    const latest = screen.getByRole('region', { name: 'Latest production outcome' })
    expect(within(latest).getByText('Rejected')).toBeInTheDocument()
    expect(latest).toHaveTextContent('Committed in Run 58 on ')
    expect(within(latest).getByRole('link', { name: 'Run 58' })).toHaveAttribute('href', '/runs/58')
    expect(screen.getByRole('region', { name: 'Queue state' })).toHaveTextContent('Not in any queue.')

    expect(screen.getByText('Simulation runs are what-if results. They never change the latest outcome or the queue state.')).toBeInTheDocument()
    const runs = screen.getByRole('region', { name: 'Runs that decided this video' })
    const items = within(runs).getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(items[0]).toHaveTextContent('Run 61 · Dry run (simulation) · Hypothetical accepted')
    expect(items[1]).toHaveTextContent('Run 58 · Scheduled · Rejected')
    expect(items[2]).toHaveTextContent('Run 40 · Run Now · Rejected')
    expect(within(runs).getByRole('link', { name: 'Open decision detail for run 61' })).toHaveAttribute('href', '/runs/61/video/dQw4w9WgXcQ')
  })

  it('renders Unavailable on YouTube as its own badge, never Rejected or Failed', async () => {
    fetchMock.mockResolvedValue(
      json(
        base({
          latest_outcome: { state: 'committed', outcome: 'unavailable_on_youtube', run_id: 7, committed_at: null },
          runs: [run(7, { outcome: 'unavailable_on_youtube' })],
        })
      )
    )
    renderAt('/videos/dQw4w9WgXcQ')
    await screen.findByRole('heading', { level: 1 })
    expect(screen.getAllByText('Unavailable on YouTube')).toHaveLength(2)
    expect(screen.queryByText('Rejected')).toBeNull()
    expect(screen.queryByText('Failed')).toBeNull()
    expect(screen.queryByRole('button', { name: /retry/i })).toBeNull()
  })

  it('shows "Re-queued by user" for a deferred item made by the re-queue action only', async () => {
    fetchMock.mockResolvedValueOnce(json(base({ queue_state: { state: 'deferred_pending', reason: 'requeued_by_user' } })))
    renderAt('/videos/dQw4w9WgXcQ')
    const queue = await screen.findByRole('region', { name: 'Queue state' })
    expect(queue).toHaveTextContent('In the deferred queue — pending. Reason: Re-queued by user.')
    expect(within(queue).getByRole('link', { name: 'View deferred queue' })).toBeInTheDocument()
  })

  it.each([
    [{ state: 'deferred_claimed', reason: 'quota_exhausted', claiming_run_id: 9 }, 'In the deferred queue — claimed by run 9. Reason: Quota exhausted.', 'View run 9'],
    [{ state: 'errors_active', category: 'x' }, 'In the Errors queue — active.', 'View Errors'],
    [{ state: 'errors_claimed', category: 'x', claiming_run_id: 4 }, 'In the Errors queue — claimed by run 4.', 'View run 4'],
    [{ state: 'removed_by_user' }, 'Removed from a queue by you. It will not be retried.', null],
  ] as const)('shows queue state %j', async (queue_state, text, link) => {
    fetchMock.mockResolvedValueOnce(json(base({ queue_state: queue_state as never })))
    renderAt('/videos/dQw4w9WgXcQ')
    const region = await screen.findByRole('region', { name: 'Queue state' })
    expect(region).toHaveTextContent(text)
    if (link) expect(within(region).getByRole('link', { name: link })).toBeInTheDocument()
    else expect(within(region).queryByRole('link')).toBeNull()
  })

  it.each([
    [{ state: 'not_yet_evaluated' }, 'Not yet evaluated. No production run has committed an outcome for this video.'],
    [{ state: 'imported_reference' }, 'Imported duplicate reference. YuHaul has never processed this video.'],
    [{ state: 'simulation_only' }, 'Simulation only. No production run has decided this video, so there is no production outcome.'],
  ] as const)('shows latest outcome %j', async (latest_outcome, text) => {
    fetchMock.mockResolvedValueOnce(json(base({ latest_outcome: latest_outcome as never })))
    renderAt('/videos/dQw4w9WgXcQ')
    expect(await screen.findByRole('region', { name: 'Latest production outcome' })).toHaveTextContent(text)
  })

  it('shows the title-unavailable copy and the channel ID, and Video {id} as H1', async () => {
    fetchMock.mockResolvedValueOnce(
      json(base({ title: { text: null, availability: 'expired' }, channel: { name: null, name_availability: 'unavailable', channel_id: 'UC77' } }))
    )
    renderAt('/videos/dQw4w9WgXcQ')
    expect(await screen.findByRole('heading', { level: 1, name: 'Video dQw4w9WgXcQ' })).toBeInTheDocument()
    expect(screen.getByText('Video title unavailable — local metadata expired')).toBeInTheDocument()
    expect(screen.getByText('Channel ID: UC77')).toBeInTheDocument()
  })

  it('shows the first 10 runs, then Show n more runs reveals the rest', async () => {
    const runs = Array.from({ length: 13 }, (_, i) => run(100 - i))
    fetchMock.mockResolvedValueOnce(json(base({ runs })))
    renderAt('/videos/dQw4w9WgXcQ')
    const region = await screen.findByRole('region', { name: 'Runs that decided this video' })
    expect(within(region).getAllByRole('listitem')).toHaveLength(10)
    fireEvent.click(screen.getByRole('button', { name: 'Show 3 more runs' }))
    expect(within(region).getAllByRole('listitem')).toHaveLength(13)
    expect(screen.queryByRole('button', { name: /Show .* more runs/ })).toBeNull()
  })

  it('says so when no run decided the video', async () => {
    fetchMock.mockResolvedValueOnce(json(base()))
    renderAt('/videos/dQw4w9WgXcQ')
    expect(await screen.findByText('No run has recorded a decision for this video.')).toBeInTheDocument()
  })

  it('shows not-found for an unknown ID (status 404) with Back to search', async () => {
    fetchMock.mockResolvedValueOnce(json({ error: 'video_not_found' }, 404))
    renderAt('/videos/aaaaaaaaaaa')
    expect(await screen.findByText('YuHaul knows no video with the ID aaaaaaaaaaa.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to search' })).toHaveAttribute('href', '/videos')
  })

  it('shows the load failure with Retry', async () => {
    fetchMock.mockResolvedValueOnce(json({ error: 'video_failed' }, 500)).mockResolvedValueOnce(json(base()))
    renderAt('/videos/dQw4w9WgXcQ')
    expect(await screen.findByText('Could not load this video. Nothing was changed.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'Lofi Beats to Study To (Live Remix)' })).toBeInTheDocument()
  })

  it('rejects a malformed ID without any request', () => {
    renderAt('/videos/short')
    expect(
      screen.getByText('“short” is not a valid video ID. A video ID has 11 characters: letters, digits, - or _.')
    ).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('writes nothing to Web Storage', async () => {
    const set = vi.spyOn(Storage.prototype, 'setItem')
    fetchMock.mockResolvedValueOnce(json(base()))
    renderAt('/videos/dQw4w9WgXcQ')
    await screen.findByRole('heading', { level: 1 })
    expect(set).not.toHaveBeenCalled()
  })
})
