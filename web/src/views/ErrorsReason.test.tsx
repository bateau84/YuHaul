import { afterEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { ErrorsView } from './ErrorsView'
import { ToastProvider } from '../components/ToastProvider'
import * as queueApi from '../api/queue'
import * as deferredPageApi from '../api/deferred-page'
import type { DeferredItem, DeferredPage, ErrorEntry } from '../types'

const QUOTA = 'diag:v1 phase=action_execution operation=execute_action code=provider_error status=403 reason=quotaExceeded'

function entry(over: Partial<ErrorEntry> = {}): ErrorEntry {
  return {
    id: 1,
    video_id: 12,
    video_title: 'Same Title',
    external_video_id: 'aaaaaaaaaaa',
    run_id: 9,
    error_category: 'api_error',
    error_message: QUOTA,
    status: 'active',
    created_at: '2026-10-01T00:00:00Z',
    retry_count: 0,
    ...over,
  }
}

function renderErrors() {
  return render(
    <MemoryRouter>
      <ToastProvider>
        <ErrorsView />
      </ToastProvider>
    </MemoryRouter>,
  )
}

afterEach(() => vi.restoreAllMocks())

describe('Errors Category and Reason presentation', () => {
  it('shows the category label and one sentence, with no diag text until Technical details is opened', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry()])
    renderErrors()
    const row = (await screen.findByText('Same Title')).closest('tr')!
    expect(within(row).getByText('YouTube API error')).toBeInTheDocument()
    expect(within(row).getByText("YouTube's API quota was used up.")).toBeInTheDocument()
    const details = row.querySelector('details')!
    expect(details.open).toBe(false)
    expect(details.querySelector('summary')).toHaveTextContent('Technical details')

    fireEvent.click(within(row).getByText('Technical details'))
    expect(details.open).toBe(true)
    expect(within(row).getByText('Support code')).toBeInTheDocument()
    // The exact stored string, once, as text (selectable), in a code element.
    const code = within(row).getByText(QUOTA)
    expect(code.tagName).toBe('CODE')
    expect(row.textContent!.split(QUOTA)).toHaveLength(2)
    // Opening moves no focus into the cell and adds no live region.
    expect(row.querySelector('[role="alert"],[role="status"],[aria-live]')).toBeNull()
  })

  it('names each disclosure by its video, so two rows with one title differ only by ID, and both contain the visible label', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: undefined, external_video_id: 'aaaaaaaaaaa' }),
      entry({ id: 2, video_title: undefined, external_video_id: 'bbbbbbbbbbb' }),
      entry({ id: 3, video_title: 'Titled', external_video_id: 'ccccccccccc' }),
    ])
    renderErrors()
    for (const name of ['Technical details for video aaaaaaaaaaa', 'Technical details for video bbbbbbbbbbb', 'Technical details for Titled']) {
      const summary = await screen.findByText('Technical details', { selector: `summary[aria-label="${name}"]` })
      expect(summary.getAttribute('aria-label')).toContain(summary.textContent!)
    }
  })

  it('does not repeat the stored string in a title, tooltip or other accessible name', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry()])
    const { container } = renderErrors()
    await screen.findByText('Same Title')
    expect(container.querySelector('[title]')).toBeNull()
    for (const el of container.querySelectorAll('[aria-label]')) expect(el.getAttribute('aria-label')).not.toContain('diag:v1')
  })

  it('reads "Reason unavailable." with no disclosure for a code outside the closed set', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry({ error_message: 'diag:v1 phase=discovery operation=discover code=brand_new_code' })])
    renderErrors()
    const row = (await screen.findByText('Same Title')).closest('tr')!
    expect(within(row).getByText('Reason unavailable.')).toBeInTheDocument()
    expect(row.textContent).not.toContain('brand_new_code')
    expect(row.querySelector('details')).toBeNull()
  })

  it('reads "Reason unavailable." for a valid run-level code, keeping the raw value only as Support code', async () => {
    const message = 'diag:v1 phase=unknown operation=discard_run code=snapshot_unavailable'
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry({ error_message: message })])
    renderErrors()
    const row = (await screen.findByText('Same Title')).closest('tr')!
    const reasonCell = row.querySelector('[data-label="Reason"]')!
    expect(reasonCell.firstElementChild).toHaveTextContent(/^Reason unavailable\.$/)
    expect(reasonCell.querySelector('details code')).toHaveTextContent(message)
  })

  it('never shows non-diag text and gives it no Technical details', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry({ error_message: 'OAuth token expired during playlist insertion.' })])
    renderErrors()
    const row = (await screen.findByText('Same Title')).closest('tr')!
    expect(within(row).getByText('Reason unavailable.')).toBeInTheDocument()
    expect(row.textContent).not.toContain('OAuth token expired')
    expect(row.querySelector('details')).toBeNull()
  })

  it('labels the three categories and reads "Error" for an unlisted one', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: 'A', error_category: 'authorization' }),
      entry({ id: 2, video_title: 'B', error_category: 'runtime' }),
      entry({ id: 3, video_title: 'C', error_category: 'mystery' as ErrorEntry['error_category'] }),
    ])
    renderErrors()
    const cell = async (title: string) => (await screen.findByText(title)).closest('tr')!.querySelector('[data-label="Category"]')!
    expect(await cell('A')).toHaveTextContent(/^Authorization$/)
    expect(await cell('B')).toHaveTextContent(/^Processing error$/)
    expect(await cell('C')).toHaveTextContent(/^Error$/)
  })

  it('keeps the empty-reason wording and the unreadable-decision row unchanged', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: 'Empty', error_message: '', error_message_unavailable: true }),
      entry({ id: 2, video_title: 'Plain', error_message: '' }),
      entry({ id: 3, video_title: 'Unreadable', error_message: 'diag:v1 phase=action_execution operation=load_run code=original_decision_unavailable' }),
    ])
    renderErrors()
    const reason = async (t: string) => (await screen.findByText(t)).closest('tr')!.querySelector('[data-label="Reason"]')!
    expect(await reason('Empty')).toHaveTextContent(/^Reason unavailable$/)
    expect(await reason('Plain')).toHaveTextContent(/^No reason provided$/)
    const row = (await screen.findByText('Unreadable')).closest('tr')!
    expect(within(row).getByText('Original decision unavailable')).toBeInTheDocument()
    expect(row.querySelector('details')).toBeNull()
  })
})

describe('Same title on several rows', () => {
  it('shows the video ID and appends it to every control name, only on the shared-title rows', async () => {
    const oauth = 'diag:v1 phase=action_execution operation=execute_action code=oauth_error'
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: 'Twin', external_video_id: 'aaaaaaaaaaa', error_message: oauth }),
      entry({ id: 2, video_title: 'Twin', external_video_id: 'bbbbbbbbbbb', closed_reason: 'final_outcome', video_final_outcome: 'accepted' }),
      entry({ id: 3, video_title: 'Solo', external_video_id: 'ccccccccccc', error_message: oauth }),
    ])
    renderErrors()
    const first = (await screen.findAllByText('Twin'))[0].closest('tr')!
    const solo = (await screen.findByText('Solo')).closest('tr')!

    expect(within(first).getByText('Video ID: aaaaaaaaaaa')).toBeInTheDocument()
    expect(within(first).getByRole('button', { name: 'Retry Twin, video aaaaaaaaaaa' })).toHaveTextContent(/^Retry$/)
    expect(within(first).getByRole('button', { name: 'Remove error: Twin, video aaaaaaaaaaa' })).toHaveTextContent(/^Remove$/)
    expect(within(first).getByRole('link', { name: 'View account status, video aaaaaaaaaaa' })).toBeInTheDocument()
    expect(within(first).getByText('Technical details')).toHaveAttribute('aria-label', 'Technical details for Twin, video aaaaaaaaaaa')

    // A closed row counts for the comparison and shows its ID too.
    expect(screen.getByText('Video ID: bbbbbbbbbbb')).toBeInTheDocument()

    expect(within(solo).queryByText(/Video ID:/)).toBeNull()
    expect(within(solo).getByRole('button', { name: 'Retry Solo' })).toBeInTheDocument()
    expect(within(solo).getByRole('button', { name: 'Remove error: Solo' })).toBeInTheDocument()
    expect(within(solo).getByRole('link', { name: 'View account status' })).toBeInTheDocument()
    expect(within(solo).getByText('Technical details')).toHaveAttribute('aria-label', 'Technical details for Solo')
  })

  it('is recomputed when the list changes: the last twin returns to its plain names', async () => {
    const rows = [entry({ id: 1, video_title: 'Twin', external_video_id: 'aaaaaaaaaaa' }), entry({ id: 2, video_title: 'Twin', external_video_id: 'bbbbbbbbbbb' })]
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue(rows)
    vi.spyOn(queueApi, 'dismissError').mockResolvedValue(rows[0])
    renderErrors()
    fireEvent.click(await screen.findByRole('button', { name: 'Remove error: Twin, video aaaaaaaaaaa' }))
    expect(await screen.findByRole('button', { name: 'Retry Twin' })).toBeInTheDocument()
    expect(screen.queryByText(/Video ID:/)).toBeNull()
  })

  it('gives Dismiss and View video of an unreadable-decision row the same suffix', async () => {
    const unreadable = 'diag:v1 phase=action_execution operation=load_run code=original_decision_unavailable'
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: 'Twin', external_video_id: 'aaaaaaaaaaa', error_message: unreadable }),
      entry({ id: 2, video_title: 'Twin', external_video_id: 'bbbbbbbbbbb' }),
    ])
    renderErrors()
    expect(await screen.findByRole('button', { name: 'Dismiss error: Twin, video aaaaaaaaaaa' })).toHaveTextContent(/^Dismiss$/)
    expect(screen.getByRole('link', { name: 'View video: Twin, video aaaaaaaaaaa' })).toBeInTheDocument()
  })
})

describe('Same title on the Deferred page', () => {
  const item = (id: number, over: Partial<DeferredItem> = {}): DeferredItem => ({
    id, video_id: 1000 + id, external_video_id: `vid${id}`, video_title: 'Twin', run_id: 5,
    deferral_reason: 'quota_exhausted', status: 'pending', created_at: '2026-11-11T00:00:00Z', retry_count: 0, ...over,
  })
  const page = (items: DeferredItem[]): DeferredPage => ({
    deferred: items, total: items.length, queue_total: items.length, by_reason: {},
    page: { limit: 50, number: 1, total_pages: 1, first_position: 1, last_position: items.length, next_after_id: null, prev_before_id: null },
  })

  it('adds the video ID line and the ", video {id}" suffix to the shared-title rows only', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([])
    vi.spyOn(deferredPageApi, 'getDeferredPage').mockResolvedValue(
      page([item(1), item(2, { closed_reason: 'final_outcome', video_final_outcome: 'accepted' }), item(3, { video_title: 'Solo' })]),
    )
    render(
      <MemoryRouter initialEntries={['/?tab=deferred']}>
        <ToastProvider>
          <ErrorsView />
        </ToastProvider>
      </MemoryRouter>,
    )
    const retry = await screen.findByRole('button', { name: 'Retry now: Twin, video vid1' })
    expect(retry).toHaveTextContent(/^Retry now$/)
    expect(screen.getByRole('button', { name: 'Dismiss deferred item: Twin, video vid1' })).toHaveTextContent(/^Dismiss$/)
    expect(screen.getByText('Video ID: vid1')).toBeInTheDocument()
    expect(screen.getByText('Video ID: vid2')).toBeInTheDocument() // closed rows count
    expect(screen.getByRole('button', { name: 'Retry now: Solo' })).toBeInTheDocument()
    expect(screen.queryByText('Video ID: vid3')).toBeNull()
  })
})

describe('Row control names use one target pattern', () => {
  const unreadable = 'diag:v1 phase=action_execution operation=load_run code=original_decision_unavailable'
  it('names untitled rows with "video {id}" for Dismiss, View video and a closed row', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: undefined, external_video_id: 'aaaaaaaaaaa', error_message: unreadable }),
      entry({ id: 2, video_title: undefined, external_video_id: 'bbbbbbbbbbb', closed_reason: 'final_outcome', video_final_outcome: 'accepted' }),
    ])
    renderErrors()
    expect(await screen.findByRole('button', { name: 'Dismiss error: video aaaaaaaaaaa' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View video: video aaaaaaaaaaa' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'View video: video bbbbbbbbbbb' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'View video' })).toBeNull()
  })

  it('names a closed row with a shared title as "{title}, video {id}"', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: 'Twin', external_video_id: 'aaaaaaaaaaa' }),
      entry({ id: 2, video_title: 'Twin', external_video_id: 'bbbbbbbbbbb', closed_reason: 'final_outcome', video_final_outcome: 'accepted' }),
    ])
    renderErrors()
    expect(await screen.findByRole('link', { name: 'View video: Twin, video bbbbbbbbbbb' })).toHaveTextContent(/^View video$/)
  })
})

describe('Account link and long titles', () => {
  const d = (code: string) => `diag:v1 phase=action_execution operation=execute_action code=${code}`
  it('offers View account status to Settings on authorization codes only', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([
      entry({ id: 1, video_title: 'A', error_message: d('oauth_error') }),
      entry({ id: 2, video_title: 'B', error_message: d('authorization_unavailable') }),
      entry({ id: 3, video_title: 'C', error_message: d('authorization_changed') }),
      entry({ id: 4, video_title: 'D', error_message: d('credential_persistence_failed') }),
      entry({ id: 5, video_title: 'E', error_message: d('timeout') }),
      entry({ id: 6, video_title: 'F', error_message: QUOTA }),
    ])
    renderErrors()
    for (const t of ['A', 'B', 'C', 'D']) {
      const row = (await screen.findByText(t)).closest('tr')!
      expect(within(row).getByRole('link', { name: 'View account status' })).toHaveAttribute('href', '/settings')
    }
    for (const t of ['E', 'F']) expect(within((await screen.findByText(t)).closest('tr')!).queryByRole('link', { name: 'View account status' })).toBeNull()
  })

  it('marks the Video cell so a long unbroken title wraps', async () => {
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue([entry({ video_title: 'x'.repeat(200) })])
    renderErrors()
    const cell = (await screen.findByText('x'.repeat(200))).closest('td')!
    expect(cell).toHaveClass('cell-video')
    expect(readFileSync(join(process.cwd(), 'src/index.css'), 'utf8')).toMatch(/\.table td\.cell-video\s*\{\s*overflow-wrap:\s*anywhere/)
  })
})

describe('Focus after a successful Retry', () => {
  it('moves focus to the next row, keeps the toast, then to the heading when no row is left', async () => {
    const rows = [entry({ id: 1, video_title: 'First' }), entry({ id: 2, video_title: 'Second', external_video_id: 'bbbbbbbbbbb' })]
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue(rows)
    vi.spyOn(queueApi, 'retryError').mockResolvedValue(rows[0])
    renderErrors()
    fireEvent.click(await screen.findByRole('button', { name: 'Retry First' }))
    await waitFor(() => expect(screen.getByText('Second').closest('tr')).toHaveFocus())
    expect(screen.getAllByText('Error queued for retry').length).toBeGreaterThan(0)

    fireEvent.click(screen.getByRole('button', { name: 'Retry Second' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Errors & Deferred' })).toHaveFocus())
  })

  // Regression: the click lands in the microtask after the load's commit, before React
  // has run that commit's passive effects. The focus request must not be consumed by
  // that earlier commit against the old list (focusing the row being removed).
  it('moves focus to the next row when Retry is clicked before the load effects have run', async () => {
    const rows = [entry({ id: 1, video_title: 'First' }), entry({ id: 2, video_title: 'Second', external_video_id: 'bbbbbbbbbbb' })]
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue(rows)
    vi.spyOn(queueApi, 'retryError').mockResolvedValue(rows[0])
    const g = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }
    const before = g.IS_REACT_ACT_ENVIRONMENT
    g.IS_REACT_ACT_ENVIRONMENT = false
    const observer = new MutationObserver(() => {
      const retry = screen.queryByRole('button', { name: 'Retry First' })
      if (retry) {
        observer.disconnect()
        retry.click()
      }
    })
    try {
      observer.observe(document.body, { childList: true, subtree: true })
      renderErrors()
      await waitFor(() => expect(screen.queryByText('First')).toBeNull())
      await waitFor(() => expect(screen.getByText('Second').closest('tr')).toHaveFocus())
      expect(queueApi.retryError).toHaveBeenCalledTimes(1)
    } finally {
      observer.disconnect()
      g.IS_REACT_ACT_ENVIRONMENT = before
    }
  })

  it('moves focus to the heading when the last of several rows is retried', async () => {
    const rows = [entry({ id: 1, video_title: 'First' }), entry({ id: 2, video_title: 'Last', external_video_id: 'bbbbbbbbbbb' })]
    vi.spyOn(queueApi, 'listErrors').mockResolvedValue(rows)
    vi.spyOn(queueApi, 'retryError').mockResolvedValue(rows[1])
    renderErrors()
    fireEvent.click(await screen.findByRole('button', { name: 'Retry Last' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Errors & Deferred' })).toHaveFocus())
    expect(screen.getByText('First')).toBeInTheDocument()
  })
})

// jsdom applies no stylesheet, so read index.css (as contrast.test.ts does).
describe('Modal/dialog surface CSS', () => {
  const css = readFileSync(join(process.cwd(), 'src/index.css'), 'utf8')
  const block = (selector: string) => {
    const re = new RegExp(`(?:^|\\n)${selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}\\s*\\{([^}]*)\\}`, 'g')
    const bodies = [...css.matchAll(re)].map((m) => m[1])
    expect(bodies.length, selector).toBeGreaterThan(0)
    return bodies.join('\n')
  }

  it('dims the page with a 50% black backdrop for both dialog kinds', () => {
    expect(block('dialog.card::backdrop')).toMatch(/background:\s*rgba\(0,\s*0,\s*0,\s*0\.5\)/)
    expect(block('.modal-backdrop')).toMatch(/background:\s*rgba\(0,\s*0,\s*0,\s*0\.5\)/)
  })

  it('uses a solid surface with a 1px edge', () => {
    for (const selector of ['dialog.card', '.modal']) {
      const b = block(selector)
      expect(b, selector).toMatch(/background:\s*var\(--color-surface-primary\)/)
      expect(b, selector).toMatch(/border:\s*1px solid var\(--color-border-default\)/)
    }
  })

  it('is at most 36rem wide, centred, and scrolls inside itself with a 16px margin a side', () => {
    expect(block('dialog.card')).toMatch(/width:\s*min\(36rem,\s*calc\(100vw - 32px\)\)/)
    expect(block('dialog.card')).toMatch(/max-width:\s*none/)
    expect(block('dialog.card')).toMatch(/margin:\s*auto/)
    expect(block('dialog.card')).toMatch(/overflow-y:\s*auto/)
    expect(block('dialog.card')).toMatch(/max-height:\s*calc\(100dvh - 32px\)/)
    const modal = block('.modal')
    expect(modal).toMatch(/max-width:\s*36rem/)
    expect(modal).toMatch(/overflow-y:\s*auto/)
    expect(block('.modal-backdrop')).toMatch(/justify-content:\s*center/)
    expect(block('.modal-backdrop')).toMatch(/padding:\s*16px/)
  })
})
