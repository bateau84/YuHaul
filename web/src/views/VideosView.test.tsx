import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { VideosView, VIDEOS_NOTICE } from './VideosView'
import type { VideoSearchPage, VideoSummary } from '../types'

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

const video = (id: string, over: Partial<VideoSummary> = {}): VideoSummary => ({
  video_id: id,
  record: 'known',
  title: { text: `Title ${id}`, availability: 'current_local' },
  channel: { name: 'ChillHop', name_availability: 'current_local', channel_id: 'UC1' },
  latest_outcome: { state: 'committed', outcome: 'rejected', run_id: 58, committed_at: '2026-11-10T10:00:00Z' },
  queue_state: { state: 'none' },
  ...over,
})

const pageOf = (over: Partial<VideoSearchPage> = {}): VideoSearchPage => ({
  items: [video('aaaaaaaaaaa')],
  total: 1,
  page_size: 50,
  number: 1,
  total_pages: 1,
  first_position: 1,
  last_position: 1,
  next_cursor: null,
  prev_cursor: null,
  ...over,
})

function Where() {
  const l = useLocation()
  return <div data-testid="where">{l.pathname + l.search}</div>
}

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/videos" element={<VideosView />} />
        <Route path="/videos/:videoId" element={<div>video view</div>} />
      </Routes>
      <Where />
    </MemoryRouter>
  )
}

let fetchMock: ReturnType<typeof vi.spyOn>
let storageSpy: ReturnType<typeof vi.spyOn>[]

beforeEach(() => {
  fetchMock = vi.spyOn(globalThis, 'fetch')
  storageSpy = [vi.spyOn(Storage.prototype, 'setItem'), vi.spyOn(Storage.prototype, 'removeItem')]
})
afterEach(() => vi.restoreAllMocks())

function submitSearch(text: string) {
  fireEvent.change(screen.getByRole('searchbox'), { target: { value: text } })
  fireEvent.submit(screen.getByRole('search'))
}

const calls = () => fetchMock.mock.calls.map((c: unknown[]) => String(c[0]))

describe('VideosView (design: Videos — Find a Video by Title or ID)', () => {
  it('shows the notice, form and empty message on load with no request', () => {
    renderAt('/videos')
    expect(screen.getByRole('heading', { level: 1, name: 'Videos' })).toBeInTheDocument()
    expect(screen.getByText(VIDEOS_NOTICE)).toBeVisible()
    expect(screen.getByRole('search')).toBeInTheDocument()
    const input = screen.getByLabelText('Search by video title, video ID or YouTube link')
    expect(input).toHaveAttribute('type', 'search')
    expect(input).toHaveAttribute('enterkeyhint', 'search')
    expect(input).not.toHaveAttribute('maxlength')
    expect(input.getAttribute('aria-describedby')).toContain('videos-notice')
    expect(screen.getByText('Type part of a video title, a video ID, or paste a YouTube link.')).toBeInTheDocument()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('does not request on keystrokes and does not request for spaces only', async () => {
    renderAt('/videos')
    submitSearch('   ')
    expect(fetchMock).not.toHaveBeenCalled()
    expect(screen.getByText(/Type part of a video title/)).toBeInTheDocument()
  })

  const kinds: [string, string, 'alert' | 'status' | null][] = [
    ['too_short', 'Type at least 3 characters, or a video ID.', 'alert'],
    ['too_long', 'The search text is too long. Use at most 200 characters.', 'alert'],
    [
      'unusable_link',
      'This link does not contain a video ID YuHaul can use. Paste a youtube.com/watch?v=…, youtu.be/… or youtube.com/shorts/… link, or the 11-character video ID.',
      'alert',
    ],
    ['empty', 'Type part of a video title, a video ID, or paste a YouTube link.', null],
  ]
  it.each(kinds)('shows the %s message', async (kind, text, role) => {
    fetchMock.mockResolvedValue(json({ kind }))
    renderAt('/videos')
    const input = screen.getByRole('searchbox')
    input.focus()
    submitSearch('lo')
    const msg = await screen.findByText(text)
    if (role) expect(msg).toHaveAttribute('role', role)
    if (role === 'alert') {
      expect(input).toHaveAttribute('aria-invalid', 'true')
      expect(input.getAttribute('aria-describedby')).toContain(msg.parentElement!.id)
      expect(input).toHaveFocus()
    }
    expect(screen.getByText(VIDEOS_NOTICE)).toBeVisible()
  })

  it('shows the ID-not-found messages (typed, and from a link) as status', async () => {
    fetchMock.mockResolvedValueOnce(json({ kind: 'id_not_found', video_id: 'dQw4w9WgXcQ', from_link: false }))
    renderAt('/videos')
    submitSearch('dQw4w9WgXcQ')
    expect(
      await screen.findByText(
        'YuHaul knows no video with the ID dQw4w9WgXcQ. Video IDs are case-sensitive, so check the letters. YuHaul searched only its own records and did not ask YouTube.'
      )
    ).toHaveAttribute('role', 'status')

    fetchMock.mockResolvedValueOnce(
      json({ kind: 'id_not_found', video_id: 'dQw4w9WgXcQ', from_link: true, results: pageOf() })
    )
        submitSearch('https://youtu.be/dQw4w9WgXcQ')
    expect(await screen.findByText(/, taken from your link\./)).toHaveAttribute('role', 'status')
    // Name results follow the message with their heading.
    expect(screen.getByRole('heading', { level: 2, name: /Videos whose titles contain/ })).toBeInTheDocument()
  })

  it('shows the no-match message with the echoed text cut at 60 characters', async () => {
    fetchMock.mockResolvedValue(json({ kind: 'results', results: pageOf({ items: [], total: 0, first_position: null, last_position: null }) }))
    const long = 'x'.repeat(70)
    renderAt(`/videos?q=${long}`)
    const msg = await screen.findByText(/No video with a held title contains/)
    expect(msg).toHaveTextContent(`“${'x'.repeat(60)}…”. Videos whose titles are no longer held can still be found by video ID or YouTube link.`)
    expect(msg).toHaveAttribute('role', 'status')
    expect(screen.queryByText(/^Showing/)).toBeNull()
  })

  it('shows typed text as plain text, never markup', async () => {
    fetchMock.mockResolvedValue(json({ kind: 'results', results: pageOf({ items: [], total: 0, first_position: null, last_position: null }) }))
    renderAt('/videos?q=' + encodeURIComponent('<b>bold</b>'))
    expect(await screen.findByText(/No video with a held title contains “<b>bold<\/b>”/)).toBeInTheDocument()
    expect(document.querySelector('b')).toBeNull()
  })

  it('shows Searching… while pending, then the failure with Retry search', async () => {
    let release!: (r: Response) => void
    fetchMock.mockReturnValueOnce(new Promise<Response>((r) => (release = r)))
    renderAt('/videos')
    submitSearch('lofi')
    const button = await screen.findByRole('button', { name: 'Searching…' })
    expect(button).toBeDisabled()
    expect(screen.getByText(VIDEOS_NOTICE)).toBeVisible()
    release(json({ error: 'search_failed' }, 500))
    expect(await screen.findByText('Could not search. Nothing was changed. Try again.')).toHaveAttribute('role', 'alert')

    fetchMock.mockResolvedValueOnce(json({ kind: 'results', results: pageOf() }))
    fireEvent.click(screen.getByRole('button', { name: 'Retry search' }))
    expect(await screen.findByText('Showing 1–1 of 1. Newest discovery first.')).toBeInTheDocument()
  })

  it('tells the user to start again when the server rejects a cursor or page', async () => {
    fetchMock.mockResolvedValue(json({ error: 'invalid_page' }, 400))
    renderAt('/videos?q=lofi&page=x')
    expect(await screen.findByText('This page of results is no longer valid. Start the search again.')).toHaveAttribute('role', 'alert')
  })

  it('lists results with the total, the item fields and the announcement', async () => {
    fetchMock.mockResolvedValue(
      json({
        kind: 'results',
        results: pageOf({
          items: [
            video('aaaaaaaaaaa'),
            video('bbbbbbbbbbb', {
              channel: { name: null, name_availability: 'expired', channel_id: 'UC9' },
              latest_outcome: { state: 'committed', outcome: 'unavailable_on_youtube', run_id: 7, committed_at: null },
              queue_state: { state: 'deferred_claimed', reason: 'requeued_by_user', claiming_run_id: 9 },
            }),
            video('ccccccccccc', { latest_outcome: { state: 'simulation_only' }, channel: { name: null, name_availability: 'unavailable', channel_id: null } }),
            video('ddddddddddd', { latest_outcome: { state: 'not_yet_evaluated' }, queue_state: { state: 'removed_by_user' } }),
          ],
          total: 4,
          last_position: 4,
        }),
      })
    )
    renderAt('/videos?q=title')
    expect(await screen.findByText('Showing 1–4 of 4. Newest discovery first.')).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 2, name: 'Videos whose titles contain “title”' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Title aaaaaaaaaaa' })).toHaveAttribute('href', '/videos/aaaaaaaaaaa')
    expect(screen.getAllByText('Current/local information')).toHaveLength(4)
    expect(screen.getByText('Video ID: bbbbbbbbbbb')).toBeInTheDocument()
    expect(screen.getAllByText('Source channel: ChillHop')).toHaveLength(2)
    expect(screen.getByText('Channel ID: UC9')).toBeInTheDocument()
    expect(screen.getByText('Source channel name unavailable')).toBeInTheDocument()
    expect(screen.getByText('Unavailable on YouTube')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Run 7' })).toHaveAttribute('href', '/runs/7')
    expect(screen.getByText('Simulation only')).toBeInTheDocument()
    expect(screen.getByText('Not yet evaluated')).toBeInTheDocument()
    expect(screen.getByText('Deferred — claimed by run 9')).toBeInTheDocument()
    expect(screen.getByText('Removed by you')).toBeInTheDocument()
    const announcement = screen.getByText('4 videos on this page.')
    expect(announcement).toHaveAttribute('role', 'status')
  })

  it('announces a single video with the singular form', async () => {
    fetchMock.mockResolvedValue(json({ kind: 'results', results: pageOf() }))
    renderAt('/videos?q=title')
    expect(await screen.findByText('1 video on this page.')).toBeInTheDocument()
  })

  it('pages both ways with cursors, writes page into the URL and focuses the heading', async () => {
    const p1 = pageOf({ total: 130, total_pages: 3, first_position: 1, last_position: 50, next_cursor: 'n1', items: [video('aaaaaaaaaaa')] })
    const p2 = pageOf({ total: 130, total_pages: 3, number: 2, first_position: 51, last_position: 100, next_cursor: 'n2', prev_cursor: 'p2', items: [video('bbbbbbbbbbb')] })
    fetchMock
      .mockResolvedValueOnce(json({ kind: 'results', results: p1 }))
      .mockResolvedValueOnce(json({ kind: 'results', results: p2 }))
      .mockResolvedValueOnce(json({ kind: 'results', results: p1 }))
    renderAt('/videos')
    submitSearch('title')
    expect(await screen.findByText('Showing 1–50 of 130. Newest discovery first.')).toBeInTheDocument()
    const nav = screen.getByRole('navigation', { name: 'Search result pages' })
    expect(nav).toHaveTextContent('Page 1')
    expect(screen.queryByRole('button', { name: 'Previous page' })).toBeNull()
    expect(screen.queryByRole('button', { name: /last page|go to page/i })).toBeNull()
    expect(screen.getByTestId('where')).toHaveTextContent('/videos?q=title')

    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    expect(await screen.findByText('Showing 51–100 of 130. Newest discovery first.')).toBeInTheDocument()
    expect(calls()[1]).toBe('/api/videos/search?q=title&cursor=n1')
    expect(screen.getByTestId('where')).toHaveTextContent('/videos?q=title&page=2')
    expect(screen.getByText('Page 2, showing 51–100.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('heading', { level: 2 })).toHaveFocus())

    fireEvent.click(screen.getByRole('button', { name: 'Previous page' }))
    expect(await screen.findByText('Showing 1–50 of 130. Newest discovery first.')).toBeInTheDocument()
    expect(calls()[2]).toBe('/api/videos/search?q=title&cursor=p2')
    expect(screen.getByTestId('where')).toHaveTextContent('/videos?q=title&page=1')
    expect(storageSpy[0]).not.toHaveBeenCalled()
  })

  it('restores a page from the URL by position on reload and never puts a cursor in the URL', async () => {
    fetchMock.mockResolvedValue(
      json({ kind: 'results', results: pageOf({ number: 3, total: 130, first_position: 101, last_position: 130, prev_cursor: 'p3' }) })
    )
    renderAt('/videos?q=title&page=3')
    expect(await screen.findByText('Showing 101–130 of 130. Newest discovery first.')).toBeInTheDocument()
    expect(calls()[0]).toBe('/api/videos/search?q=title&page=3')
    expect(screen.queryByRole('button', { name: 'Next page' })).toBeNull()
    expect(screen.getByTestId('where').textContent).not.toContain('cursor')
  })

  it('offers Go to page 1 for an empty page', async () => {
    fetchMock
      .mockResolvedValueOnce(json({ kind: 'results', results: pageOf({ items: [], number: 4, total: 130, first_position: null, last_position: null }) }))
      .mockResolvedValueOnce(json({ kind: 'results', results: pageOf() }))
    renderAt('/videos?q=title&page=4')
    expect(
      await screen.findByText('This page has no results. Held titles may have changed since this search began. Go to page 1.')
    ).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Go to page 1' }))
    expect(await screen.findByText('Showing 1–1 of 1. Newest discovery first.')).toBeInTheDocument()
    expect(screen.getByTestId('where')).toHaveTextContent('/videos?q=title')
  })

  it('replaces the URL with the video view on kind: video', async () => {
    fetchMock.mockResolvedValue(json({ kind: 'video', video_id: 'dQw4w9WgXcQ', from_link: true }))
    renderAt('/videos')
    submitSearch('https://youtu.be/dQw4w9WgXcQ?t=42')
    expect(await screen.findByText('video view')).toBeInTheDocument()
    expect(screen.getByTestId('where')).toHaveTextContent('/videos/dQw4w9WgXcQ')
    // replace: Back does not return to the search that redirected.
    expect(calls()).toEqual(['/api/videos/search?q=https%3A%2F%2Fyoutu.be%2FdQw4w9WgXcQ%3Ft%3D42'])
  })

  it('trims only the ends of the text and keeps inner spaces', async () => {
    fetchMock.mockResolvedValue(json({ kind: 'results', results: pageOf() }))
    renderAt('/videos')
    submitSearch('  lofi  beats ')
    await screen.findByText('Showing 1–1 of 1. Newest discovery first.')
    expect(calls()[0]).toBe('/api/videos/search?q=lofi++beats')
  })
})
