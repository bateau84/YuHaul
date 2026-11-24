import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom'
import { ErrorsView } from './ErrorsView'
import { RequeueStrandedView } from './RequeueStrandedView'
import { ToastProvider } from '../components/ToastProvider'
import * as queueApi from '../api/queue'
import * as deferredPageApi from '../api/deferred-page'
import { requeueRefusalCodes } from '../types'
import type { ErrorEntry, RequeueResult, StrandedPreview, StrandedReport } from '../types'

// X2 (ARS-005 §11.9): every preview field, the confirm body, every refusal code,
// the difference message and the labels. The real API client and ApiError run;
// only the HTTP responses are stubbed, so the page is exercised through the same
// seam production uses. The page branches on status and code, never on text.

const realFetch = globalThis.fetch

interface Reply {
  status?: number
  body: unknown
}
interface Call {
  method: string
  url: string
  body?: string
}

let calls: Call[]
let onGet: () => Reply | Promise<Reply>
let onPost: (body: string | undefined) => Reply | Promise<Reply> | never

const gets = () => calls.filter((c) => c.method === 'GET' && c.url === '/api/stranded-videos')
const posts = () => calls.filter((c) => c.method === 'POST')

function reply({ status = 200, body }: Reply): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

function report(over: Partial<StrandedReport> = {}): StrandedReport {
  return {
    requeue: 69000,
    already_queued: { videos: 1180, in_errors_queue: 2 },
    removed_by_user: 3,
    blocked: {
      videos: 41,
      by_reason: { uncertain_addition: 2, unresolved_send_guard: 4, ambiguous_legacy_evidence: 35 },
      items: [
        { video_id: 812, youtube_video_id: 'dQw4w9WgXcQ', reasons: [{ reason: 'ambiguous_legacy_evidence', run_id: 14 }] },
        {
          video_id: 813,
          youtube_video_id: 'aaaaaaaaaaa',
          reasons: [
            { reason: 'uncertain_addition', run_id: 52 },
            { reason: 'unresolved_send_guard', run_id: 0 },
          ],
        },
      ],
    },
    not_ended: { videos: 12, runs: [{ run_id: 52, videos: 12 }] },
    no_origin_run: 0,
    retained_without_queue_item: { videos: 150, removed_by_user: 40 },
    ...over,
  }
}

const preview = (over: Partial<StrandedPreview> = {}): StrandedPreview => ({
  available: true,
  unavailable_reason: null,
  ...report(),
  ...over,
})

const result = (over: Partial<RequeueResult> = {}): RequeueResult => ({
  outcome: 'requeued',
  requeued: 69000,
  skipped_concurrently: 0,
  expected_requeue: 69000,
  differs_from_preview: false,
  ...report(),
  ...over,
})

function Where() {
  return <div data-testid="where">{useLocation().pathname + useLocation().search}</div>
}

function renderAt(path = '/errors/deferred/requeue') {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={[path]}>
        <Where />
        <Routes>
          <Route path="/errors" element={<ErrorsView />} />
          <Route path="/errors/deferred/requeue" element={<RequeueStrandedView />} />
          <Route path="/runs" element={<h1>Runs list</h1>} />
          <Route path="/runs/:id" element={<h1>Run detail</h1>} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>,
  )
}

async function ready() {
  await screen.findByRole('table', { name: 'Preview counts' })
}

function openDialog(n = '69,000 videos') {
  fireEvent.click(screen.getByRole('button', { name: `Re-queue ${n}` }))
  return screen.getByRole('dialog', { name: `Re-queue ${n}?` })
}

beforeEach(() => {
  calls = []
  onGet = () => ({ body: preview() })
  onPost = () => ({ body: result() })
  vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    if (!url.startsWith('/api/stranded-videos')) return realFetch(input, init)
    const method = init?.method?.toUpperCase() ?? 'GET'
    calls.push({ method, url, body: typeof init?.body === 'string' ? init.body : undefined })
    return reply(method === 'POST' ? await onPost(typeof init?.body === 'string' ? init.body : undefined) : await onGet())
  })
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('Deferred toolbar control', () => {
  it('offers "Re-queue stranded videos" on the Deferred tab and opens the preview page without a write', async () => {
    renderAt('/errors?tab=deferred')
    const link = await screen.findByRole('link', { name: 'Re-queue stranded videos' })
    expect(link).toHaveAttribute('href', '/errors/deferred/requeue')
    expect(calls).toHaveLength(0)
    fireEvent.click(link)
    expect(await screen.findByRole('heading', { level: 1, name: 'Re-queue stranded videos' })).toBeInTheDocument()
    expect(screen.getByTestId('where')).toHaveTextContent('/errors/deferred/requeue')
    await ready()
    expect(posts()).toHaveLength(0)
    expect(gets()).toHaveLength(1)
  })

  it('shows for an empty queue too, because a stranded video is in no queue', async () => {
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue({
      deferred: [],
      total: 0,
      queue_total: 0,
      by_reason: {},
      page: { limit: 50, number: 1, total_pages: 1, first_position: null, last_position: null, next_after_id: null, prev_before_id: null },
    })
    renderAt('/errors?tab=deferred')
    expect(await screen.findByText('No deferred work. All pending items have been processed.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Re-queue stranded videos' })).toHaveAttribute('href', '/errors/deferred/requeue')
  })

  it('is not on the Errors tab', async () => {
    renderAt('/errors')
    await screen.findByRole('heading', { level: 1, name: /Errors/ })
    expect(screen.queryByRole('link', { name: 'Re-queue stranded videos' })).toBeNull()
  })
})

describe('Preview page', () => {
  it('counts first, announces once when ready, and renders every field of the preview', async () => {
    let release!: (r: Reply) => void
    onGet = () => new Promise<Reply>((r) => (release = r))
    renderAt()
    expect(screen.getByRole('heading', { level: 1, name: 'Re-queue stranded videos' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Back to Deferred' })).toHaveAttribute('href', '/errors?tab=deferred')
    expect(screen.getByText('Counting videos…')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Re-queue \d/ })).toBeNull()
    release({ body: preview() })
    await ready()
    expect(document.title).toBe('Re-queue stranded videos — YuHaul')
    expect(screen.getByText('Preview ready: 69,000 videos will be re-queued.')).toBeInTheDocument()
    expect(screen.queryByText('Counting videos…')).toBeNull()

    expect(screen.getByText(/A stranded video was found by YuHaul, has no final outcome, is in no queue/)).toBeInTheDocument()
    expect(screen.getByText('69,000 videos will be re-queued.')).toBeInTheDocument()
    expect(
      screen.getByText(
        "The next new run, from Run Now or the schedule, will use as much of each day's quota as these videos need. Newly uploaded videos may wait. This can take more than one day.",
      ),
    ).toBeInTheDocument()
    expect(screen.getByText(/Counts read at .+\. They are checked again when you confirm\./)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Refresh counts' })).toBeInTheDocument()

    const table = screen.getByRole('table', { name: 'Preview counts' })
    const headers = within(table).getAllByRole('rowheader').map((h) => h.textContent)
    expect(headers).toEqual([
      'Will be re-queued: 69,000',
      'Blocked: 41',
      'Unresolved send guard: 4',
      'Uncertain playlist addition: 2',
      'Unclear history from an older version of YuHaul: 35',
      'Held by a run that has not ended: 12',
      'Removed by you: 3',
      'Already in a queue: 1,180',
      'In the deferred queue: 1,178',
      'In the Errors queue: 2',
      'Deferred or failed earlier, in no queue: 150',
    ])
    for (const helper of [
      'YuHaul will not re-queue these, because sending them again could add a video to a playlist twice.',
      'These videos belong to a run that has not ended. Re-queue them after it ends.',
      'You dismissed these from a queue. They stay out.',
      'Videos in the deferred queue are already waiting. Videos in the Errors queue stay there until you retry them.',
      'Counted only. These already have a deferred or failed result, and re-queue does not change them.',
    ]) {
      expect(table).toHaveTextContent(helper)
    }
    // A run that cannot be told apart from a live one: the response carries no
    // per-run endable, so every run reads "has not ended" (design gap 2).
    expect(table).toHaveTextContent('Run 52: 12 videos. Run 52 has not ended and can still continue them.')
    expect(within(table).getByRole('link', { name: 'Open run 52' })).toHaveAttribute('href', '/runs/52')
  })

  it('keeps a zero row visible and never hides a category', async () => {
    onGet = () =>
      ({
        body: preview({
          removed_by_user: 0,
          already_queued: { videos: 0, in_errors_queue: 0 },
          not_ended: { videos: 0, runs: [] },
          retained_without_queue_item: { videos: 0, removed_by_user: 0 },
          blocked: { videos: 0, by_reason: {}, items: [] },
        }),
      })
    renderAt()
    await ready()
    const headers = within(screen.getByRole('table', { name: 'Preview counts' })).getAllByRole('rowheader').map((h) => h.textContent)
    expect(headers).toContain('Blocked: 0')
    expect(headers).toContain('Unresolved send guard: 0')
    expect(headers).toContain('Held by a run that has not ended: 0')
    expect(headers).toContain('Removed by you: 0')
    expect(headers).toContain('In the Errors queue: 0')
    expect(headers).toContain('Deferred or failed earlier, in no queue: 0')
    expect(screen.queryByRole('heading', { name: /Blocked videos/ })).toBeNull()
  })

  it('uses singular words for one video', async () => {
    onGet = () => ({ body: preview({ requeue: 1 }) })
    renderAt()
    await ready()
    expect(screen.getByText('1 video will be re-queued.')).toBeInTheDocument()
    expect(screen.getByText('Preview ready: 1 video will be re-queued.')).toBeInTheDocument()
    const dialog = openDialog('1 video')
    expect(within(dialog).getByText('YuHaul will add 1 video to the deferred queue. Each will be eligible for the next run at once.')).toBeInTheDocument()
  })

  it('lists the first 10 held runs and reveals the rest on request', async () => {
    const runs = Array.from({ length: 12 }, (_, i) => ({ run_id: i + 1, videos: i + 1 }))
    onGet = () => ({ body: preview({ not_ended: { videos: 78, runs } }) })
    renderAt()
    await ready()
    expect(screen.getAllByRole('link', { name: /^Open run / })).toHaveLength(10)
    expect(screen.queryByRole('link', { name: 'Open run 11' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Show 2 more runs' }))
    expect(screen.getAllByRole('link', { name: /^Open run / })).toHaveLength(12)
    expect(screen.getByText(/Run 12: 12 videos\./)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /more runs/ })).toBeNull()
  })

  it('names each blocked video with its reason, a safe sentence and its evidence run', async () => {
    renderAt()
    await ready()
    expect(screen.getByRole('heading', { level: 2, name: 'Blocked videos (41)' })).toBeInTheDocument()
    expect(screen.getByText(/YuHaul will not re-queue these videos, and this screen cannot override that\./)).toBeInTheDocument()
    const rows = within(screen.getByRole('table', { name: 'Blocked videos (41)' })).getAllByRole('row')
    // header + two videos
    expect(rows).toHaveLength(3)
    const [, first, second] = rows
    expect(first).toHaveTextContent('Video title unavailable')
    expect(within(first).getByRole('link', { name: 'dQw4w9WgXcQ' })).toHaveAttribute('href', '/videos/dQw4w9WgXcQ')
    expect(first).toHaveTextContent('Unclear history from an older version of YuHaul')
    expect(first).toHaveTextContent('An older run may have sent an addition and left no clear result.')
    expect(within(first).getByRole('link', { name: 'Evidence from run 14' })).toHaveAttribute('href', '/runs/14')
    expect(second).toHaveTextContent('Uncertain playlist addition')
    expect(second).toHaveTextContent('A playlist addition was sent and its outcome is not known.')
    expect(second).toHaveTextContent('Unresolved send guard')
    expect(second).toHaveTextContent('A playlist addition may already have been sent.')
    expect(within(second).getByRole('link', { name: 'Evidence from run 52' })).toBeInTheDocument()
    // run_id 0: no evidence run is claimed.
    expect(second).toHaveTextContent('Run unavailable')
  })

  it('shows an unknown block reason as plain "Blocked", never the raw value', async () => {
    onGet = () =>
      ({
        body: preview({
          blocked: {
            videos: 1,
            by_reason: {},
            items: [{ video_id: 1, youtube_video_id: 'zzzzzzzzzzz', reasons: [{ reason: 'sk_live_SECRET' as never, run_id: 3 }] }],
          },
        }),
      })
    renderAt()
    await ready()
    expect(screen.getByRole('table', { name: 'Blocked videos (1)' })).toHaveTextContent('Blocked')
    expect(screen.queryByText(/sk_live_SECRET/)).toBeNull()
  })

  it('pages the blocked list 50 at a time and hides controls that cannot apply', async () => {
    const items = Array.from({ length: 120 }, (_, i) => ({
      video_id: i + 1,
      youtube_video_id: `vid${String(i + 1).padStart(8, '0')}`,
      reasons: [{ reason: 'unresolved_send_guard' as const, run_id: 9 }],
    }))
    onGet = () => ({ body: preview({ blocked: { videos: 120, by_reason: { unresolved_send_guard: 120 }, items } }) })
    renderAt()
    await ready()
    const nav = screen.getByRole('navigation', { name: 'Blocked videos pages' })
    expect(within(nav).getByText('Page 1 of 3')).toBeInTheDocument()
    expect(within(nav).queryByRole('button', { name: 'Previous page' })).toBeNull()
    expect(screen.getAllByRole('link', { name: /^vid/ })).toHaveLength(50)
    fireEvent.click(within(nav).getByRole('button', { name: 'Next page' }))
    expect(within(nav).getByText('Page 2 of 3')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'vid00000051' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Blocked videos (120)' })).toHaveFocus()
    expect(screen.getByText('Page 2 of 3, showing 51–100.')).toBeInTheDocument()
    fireEvent.click(within(nav).getByRole('button', { name: 'Next page' }))
    expect(screen.getAllByRole('link', { name: /^vid/ })).toHaveLength(20)
    expect(within(nav).queryByRole('button', { name: 'Next page' })).toBeNull()
    fireEvent.click(within(nav).getByRole('button', { name: 'Previous page' }))
    expect(within(nav).getByText('Page 2 of 3')).toBeInTheDocument()
  })

  it('survives a response whose empty collections arrive as null', async () => {
    onGet = () =>
      ({
        body: {
          ...preview(),
          blocked: { videos: 0, by_reason: null, items: null },
          not_ended: { videos: 0, runs: null },
        },
      })
    renderAt()
    await ready()
    expect(screen.getByText('Blocked: 0')).toBeInTheDocument()
  })

  it('says nothing is to be re-queued, with no headline, quota notice or confirm button', async () => {
    onGet = () => ({ body: preview({ requeue: 0 }) })
    renderAt()
    await ready()
    expect(screen.getByText('Nothing to re-queue. There are no stranded videos right now. Nothing was changed.')).toBeInTheDocument()
    expect(screen.getByText('Preview ready: nothing to re-queue.')).toBeInTheDocument()
    expect(screen.queryByText(/videos will be re-queued\./)).toBeNull()
    expect(screen.queryByText(/use as much of each day/)).toBeNull()
    expect(screen.queryByRole('button', { name: /^Re-queue / })).toBeNull()
    // The counts and the blocked list stay.
    expect(screen.getByRole('table', { name: 'Preview counts' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Blocked videos (41)' })).toBeInTheDocument()
  })

  it.each([
    [
      'production_run_active',
      'Re-queue did nothing. A production run is running, so no videos were re-queued. Nothing was changed. Wait for it to finish, then try again.',
    ],
    [
      'startup_recovery_active',
      'Re-queue did nothing. YuHaul is starting up and recovering runs, so no videos were re-queued. Nothing was changed. Try again in a minute.',
    ],
    [
      'shutting_down',
      'Re-queue did nothing. YuHaul is shutting down, so no videos were re-queued. Nothing was changed. Try again after it has started again.',
    ],
  ] as const)('when the preview says %s, the button is aria-disabled and described by the sentence, and opens nothing', async (reason, sentence) => {
    onGet = () => ({ body: preview({ available: false, unavailable_reason: reason }) })
    renderAt()
    await ready()
    const button = screen.getByRole('button', { name: 'Re-queue 69,000 videos' })
    expect(button).toHaveAttribute('aria-disabled', 'true')
    expect(button).not.toBeDisabled()
    expect(button).toHaveAccessibleDescription(expect.stringContaining(sentence))
    expect(screen.getAllByText(sentence, { exact: false }).length).toBeGreaterThan(0)
    fireEvent.click(button)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(posts()).toHaveLength(0)
  })

  it('refresh re-reads without writing, keeps the counts, and reports a failed re-read', async () => {
    renderAt()
    await ready()
    expect(gets()).toHaveLength(1)
    onGet = () => ({ body: preview({ requeue: 68000 }) })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh counts' }))
    expect(await screen.findByText('68,000 videos will be re-queued.')).toBeInTheDocument()
    expect(gets()).toHaveLength(2)
    expect(posts()).toHaveLength(0)

    onGet = () => ({ status: 500, body: { error: 'x', code: 'preview_failed' } })
    fireEvent.click(screen.getByRole('button', { name: 'Refresh counts' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not count the stranded videos again. Nothing was changed.')
    expect(screen.getByText('68,000 videos will be re-queued.')).toBeInTheDocument()
  })

  it('shows a failed first read as a recoverable error with Retry, and nothing was written', async () => {
    onGet = () => ({ status: 500, body: { error: 'the stranded videos could not be counted', code: 'preview_failed' } })
    renderAt()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not count the stranded videos. Nothing was changed.')
    expect(screen.queryByRole('table', { name: 'Preview counts' })).toBeNull()
    onGet = () => ({ body: preview() })
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    await ready()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(posts()).toHaveLength(0)
  })
})

describe('Confirmation', () => {
  it('opens a modal with the facts, focuses Cancel, and sends nothing yet', async () => {
    renderAt()
    await ready()
    const dialog = openDialog()
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus()
    expect(dialog).toHaveAccessibleDescription(expect.stringContaining('YuHaul will add 69,000 videos to the deferred queue.'))
    for (const fact of [
      "The next new run, from Run Now or the schedule, will process them as far as each day's quota allows. It may use the whole day's quota, so newly uploaded videos may wait, and this can take more than one day.",
      'Nothing is sent to YouTube now, and no run is started.',
      'Blocked videos, videos held by a run that has not ended, videos you removed, and videos already in a queue are not re-queued.',
      'The counts are checked again when you confirm. If they have changed, you will be told.',
      'There is no undo for the whole batch. Each item can be dismissed on its own from the Deferred tab.',
    ]) {
      expect(within(dialog).getByText(fact)).toBeInTheDocument()
    }
    expect(posts()).toHaveLength(0)
  })

  it.each([
    ['Cancel', (d: HTMLElement) => fireEvent.click(within(d).getByRole('button', { name: 'Cancel' }))],
    ['Escape', () => fireEvent.keyDown(document, { key: 'Escape' })],
    ['a click outside', (d: HTMLElement) => fireEvent.click(d.parentElement as HTMLElement)],
  ])('%s closes the dialog, changes nothing and returns focus to the button', async (_name, close) => {
    renderAt()
    await ready()
    const dialog = openDialog()
    close(dialog)
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(posts()).toHaveLength(0)
    await waitFor(() => expect(screen.getByRole('button', { name: 'Re-queue 69,000 videos' })).toHaveFocus())
  })

  it('sends expected_requeue once, shows the busy state with both buttons disabled, then the acknowledgement', async () => {
    let finish!: (r: Reply) => void
    onPost = () => new Promise<Reply>((r) => (finish = r)) as never
    renderAt()
    await ready()
    const dialog = openDialog()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Re-queue 69,000 videos' }))
    await waitFor(() => expect(posts()).toHaveLength(1))
    expect(posts()[0].url).toBe('/api/stranded-videos/requeue')
    expect(posts()[0].body).toBe('{"expected_requeue":69000}')
    const busy = within(dialog).getByRole('button', { name: 'Re-queuing…' })
    expect(busy).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeDisabled()
    expect(dialog.firstElementChild).toHaveAttribute('aria-busy', 'true')
    // A second press, and Escape, cannot send or close while pending.
    fireEvent.click(busy)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(posts()).toHaveLength(1)

    finish({ body: result() })
    const ackText = await screen.findByText(/^Re-queued 69,000 videos\. Each is now in the deferred queue/)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    const panel = ackText.closest('[role="status"]') as HTMLElement
    expect(panel).toHaveTextContent(
      'Re-queued 69,000 videos. Each is now in the deferred queue with the reason “Re-queued by user”, eligible for the next run. The next new run will process them as far as quota allows. Nothing was sent to YouTube and no run was started.',
    )
    expect(panel).toHaveFocus()
    expect(within(panel).getByRole('link', { name: 'View deferred queue' })).toHaveAttribute('href', '/errors?tab=deferred')
    // The preview's headline and button are gone; the result counts and the blocked list stay.
    expect(screen.queryByText('69,000 videos will be re-queued.')).toBeNull()
    expect(screen.queryByRole('button', { name: /^Re-queue \d/ })).toBeNull()
    const table = screen.getByRole('table', { name: 'Result counts' })
    expect(within(table).getAllByRole('rowheader')[0]).toHaveTextContent('Re-queued: 69,000')
    expect(screen.getByRole('heading', { name: 'Blocked videos (41)' })).toBeInTheDocument()
    expect(posts()).toHaveLength(1)
  })

  it('says when the number differs from the preview', async () => {
    onPost = () => ({ body: result({ requeued: 68998, skipped_concurrently: 2, differs_from_preview: true }) })
    renderAt()
    await ready()
    fireEvent.click(within(openDialog()).getByRole('button', { name: 'Re-queue 69,000 videos' }))
    const text = await screen.findByText(/^Re-queued 68,998 videos\. The preview showed 69,000/)
    expect(text).toHaveTextContent(
      'Re-queued 68,998 videos. The preview showed 69,000, because the videos changed between the preview and your confirmation. Each is now in the deferred queue with the reason “Re-queued by user”. Nothing was sent to YouTube and no run was started.',
    )
  })

  it('reports a set that became empty after the preview, and a repeat can preview again', async () => {
    onPost = () => ({ body: result({ outcome: 'nothing_to_requeue', requeued: 0, expected_requeue: 69000, differs_from_preview: true, requeue: 0 }) })
    renderAt()
    await ready()
    fireEvent.click(within(openDialog()).getByRole('button', { name: 'Re-queue 69,000 videos' }))
    const text = await screen.findByText('Nothing to re-queue. The stranded videos in the preview are no longer stranded. Nothing was changed.')
    expect(text.closest('[role="status"]')).toHaveFocus()
    expect(screen.queryByText(/^Re-queued /)).toBeNull()

    onGet = () => ({ body: preview({ requeue: 0 }) })
    fireEvent.click(screen.getByRole('button', { name: 'Preview again' }))
    await screen.findByText('Nothing to re-queue. There are no stranded videos right now. Nothing was changed.')
    expect(gets()).toHaveLength(2)
    expect(screen.getByRole('heading', { level: 1 })).toHaveFocus()
  })

  it.each([
    [
      409,
      'production_run_active',
      'Re-queue did nothing. A production run is running, so no videos were re-queued. Nothing was changed. Wait for it to finish, then try again.',
    ],
    [
      503,
      'startup_recovery_active',
      'Re-queue did nothing. YuHaul is starting up and recovering runs, so no videos were re-queued. Nothing was changed. Try again in a minute.',
    ],
    [
      503,
      'shutting_down',
      'Re-queue did nothing. YuHaul is shutting down, so no videos were re-queued. Nothing was changed. Try again after it has started again.',
    ],
    [
      503,
      undefined,
      "Re-queue did nothing. YuHaul can't re-queue videos right now. It may be starting up or shutting down. Nothing was changed. Try again in a minute.",
    ],
    [
      500,
      'requeue_failed',
      'Re-queue did nothing. The videos could not be saved to the deferred queue, so none was re-queued. Nothing was changed. You can try again.',
    ],
  ])('refusal %i %s keeps the dialog open with the message in a role=alert and re-enabled buttons', async (status, code, sentence) => {
    // The message text the server sends is deliberately unrelated: the page
    // branches on status and code only.
    onPost = () => ({ status, body: { error: 'unrelated text', ...(code ? { code } : {}) } })
    renderAt()
    await ready()
    const dialog = openDialog()
    fireEvent.click(within(dialog).getByRole('button', { name: 'Re-queue 69,000 videos' }))
    const alert = await within(dialog).findByRole('alert')
    expect(alert).toHaveTextContent(sentence)
    expect(screen.queryByText(/unrelated text/)).toBeNull()
    expect(within(dialog).getByRole('button', { name: 'Re-queue 69,000 videos' })).toBeEnabled()
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeEnabled()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    if (code === 'production_run_active') {
      expect(within(alert).getByRole('link', { name: 'View runs' })).toHaveAttribute('href', '/runs')
    }
    expect(posts()).toHaveLength(1)
  })

  it.each([
    ['requeue_outcome_unknown', 500, { error: 'x', code: 'requeue_outcome_unknown' }],
    ['invalid_body', 400, { error: 'x', code: 'invalid_body' }],
    ['a 500 with no code', 500, { error: 'x' }],
    ['a refusal code on the wrong status', 404, { error: 'x', code: 'production_run_active' }],
    ['a 409 with another code', 409, { error: 'a production run is already active', code: 'something_else' }],
  ])('%s closes the dialog, asks the person to check the queue, and never retries by itself', async (_name, status, body) => {
    onPost = () => ({ status, body })
    renderAt()
    await ready()
    fireEvent.click(within(openDialog()).getByRole('button', { name: 'Re-queue 69,000 videos' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(
      'Could not confirm whether the videos were re-queued. Open the deferred queue to check. If no items show “Re-queued by user”, you can try again; repeating it is safe.',
    )
    expect(within(alert).getByRole('link', { name: 'View deferred queue' })).toHaveAttribute('href', '/errors?tab=deferred')
    expect(screen.queryByRole('dialog')).toBeNull()
    expect(posts()).toHaveLength(1)
  })

  it('treats a network failure like an unknown outcome', async () => {
    onPost = () => {
      throw new TypeError('network down')
    }
    renderAt()
    await ready()
    fireEvent.click(within(openDialog()).getByRole('button', { name: 'Re-queue 69,000 videos' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Could not confirm whether the videos were re-queued.')
    expect(screen.queryByText(/network down/)).toBeNull()
    expect(posts()).toHaveLength(1)
  })

  it('every closed refusal code of the API contract is handled by a message of its own or the check-the-queue message', () => {
    // Guards the table above against a code being added to the contract unhandled.
    expect([...requeueRefusalCodes].sort()).toEqual(
      ['invalid_body', 'production_run_active', 'requeue_failed', 'requeue_outcome_unknown', 'shutting_down', 'startup_recovery_active'].sort(),
    )
  })
})

describe('Errors queue: a retried video YouTube did not return', () => {
  it('shows the design reason and no raw diagnostic text', async () => {
    const entry: ErrorEntry = {
      id: 1,
      video_id: 12,
      video_title: 'Gone Video',
      external_video_id: 'aaaaaaaaaaa',
      run_id: 9,
      error_category: 'api_error',
      error_message: 'diag:v1 phase=routing operation=load_metadata code=video_unavailable',
      status: 'active',
      created_at: '2026-10-01T00:00:00Z',
      retry_count: 1,
    }
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry])
    renderAt('/errors')
    const row = (await screen.findByText('Gone Video')).closest('tr') as HTMLElement
    expect(within(row).getByText('YouTube did not return this video when YuHaul asked for its details.')).toBeInTheDocument()
    const details = row.querySelector('details') as HTMLDetailsElement
    expect(details.open).toBe(false)
  })
})
