import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter, useLocation } from 'react-router-dom'
import { ErrorsView } from './ErrorsView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import { deferredReasons } from '../types'
import type { DeferredItem, DeferredReason } from '../types'

// G4 (ARS-005 §7.3): the Deferred tab loads one page, shows the total and
// positions, pages both ways, jumps with First/Last/Go to page, keeps its state
// in the URL, filters by reason on the server, and keeps labels and Dismiss.
// The mock behind /api/deferred pages exactly as the server does (ARS-005 §7.1).

function item(id: number, reason: DeferredReason | string = 'requeued_by_user', extra: Partial<DeferredItem> = {}): DeferredItem {
  return {
    id,
    video_id: 1000 + id,
    video_title: `Video ${id}`,
    run_id: 5,
    deferral_reason: reason,
    status: 'pending',
    created_at: '2026-11-11T00:00:00Z',
    retry_count: 0,
    ...extra,
  }
}

// 120 items: ids 1..120; ids 10, 20 and 30 are quota_exhausted.
function seed(n = 120): DeferredItem[] {
  return Array.from({ length: n }, (_, i) => item(i + 1, [10, 20, 30].includes(i + 1) ? 'quota_exhausted' : 'requeued_by_user'))
}

function Where() {
  const loc = useLocation()
  return <div data-testid="where">{loc.search}</div>
}

function renderAt(search: string) {
  return render(
    <MemoryRouter initialEntries={[`/errors${search}`]}>
      <ToastProvider>
        <ErrorsView />
        <Where />
      </ToastProvider>
    </MemoryRouter>,
  )
}

const where = () => screen.getByTestId('where').textContent

async function openDeferred(search = '?tab=deferred') {
  renderAt(search)
  await screen.findByRole('heading', { name: 'Deferred queue' })
}

beforeEach(() => mock.resetMockDeferred(seed()))
afterEach(() => vi.restoreAllMocks())

describe('Deferred tab paging (G4)', () => {
  it('loads one page of 50, shows the total in the tab and the count line', async () => {
    const spy = vi.spyOn(globalThis, 'fetch')
    await openDeferred()
    expect(screen.getByRole('tab', { name: 'Deferred (120)' })).toBeInTheDocument()
    expect(screen.getByText('Showing 1–50 of 120 items.')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^Dismiss deferred item/ })).toHaveLength(50)
    const deferredCalls = spy.mock.calls.map((c) => String(c[0])).filter((u) => u.includes('/api/deferred'))
    expect(deferredCalls).toEqual(['/api/deferred?limit=50'])
  })

  it('pages forward and back with cursors and hides controls that cannot apply', async () => {
    await openDeferred()
    const nav = screen.getByRole('navigation', { name: 'Deferred queue pages' })
    expect(within(nav).queryByRole('button', { name: 'First page' })).not.toBeInTheDocument()
    expect(within(nav).queryByRole('button', { name: 'Previous page' })).not.toBeInTheDocument()
    expect(within(nav).getByText('Page 1 of 3')).toBeInTheDocument()

    fireEvent.click(within(nav).getByRole('button', { name: 'Next page' }))
    await screen.findByText('Showing 51–100 of 120 items.')
    expect(where()).toBe('?tab=deferred&after_id=50')
    expect(screen.getByText('Video 51')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Deferred queue' })).toHaveFocus())
    expect(await screen.findByText('Page 2 of 3, showing 51–100.')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Next page' }))
    await screen.findByText('Showing 101–120 of 120 items.')
    expect(screen.queryByRole('button', { name: 'Next page' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Last page' })).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Previous page' }))
    await screen.findByText('Showing 51–100 of 120 items.')
    expect(where()).toBe('?tab=deferred&before_id=101')
  })

  it('jumps with Last page, First page and Go to page, and validates the number', async () => {
    await openDeferred()
    fireEvent.click(screen.getByRole('button', { name: 'Last page' }))
    await screen.findByText('Showing 101–120 of 120 items.')
    expect(where()).toBe('?tab=deferred&page=last')

    fireEvent.click(screen.getByRole('button', { name: 'First page' }))
    await screen.findByText('Showing 1–50 of 120 items.')
    expect(where()).toBe('?tab=deferred&page=1')

    const field = screen.getByLabelText('Go to page')
    fireEvent.change(field, { target: { value: '9' } })
    fireEvent.click(screen.getByRole('button', { name: 'Go' }))
    expect(screen.getByRole('alert')).toHaveTextContent('Enter a page from 1 to 3.')
    expect(field).toHaveAttribute('aria-describedby', 'deferred-go-to-page-error')
    expect(where()).toBe('?tab=deferred&page=1')

    fireEvent.change(field, { target: { value: '2' } })
    fireEvent.click(screen.getByRole('button', { name: 'Go' }))
    await screen.findByText('Showing 51–100 of 120 items.')
    expect(where()).toBe('?tab=deferred&page=2')
    expect(screen.queryByText(/Enter a page/)).not.toBeInTheDocument()
  })

  it('restores the page and the filter from the URL on load', async () => {
    await openDeferred('?tab=deferred&page=2&reason=requeued_by_user')
    // 117 re-queued items: page 2 is positions 51–100.
    expect(await screen.findByText('Showing 51–100 of 117 items with this reason. 120 items in the deferred queue.')).toBeInTheDocument()
    expect(screen.getByLabelText('Reason')).toHaveValue('requeued_by_user')
    expect(screen.getByRole('tab', { name: 'Deferred (120)' })).toBeInTheDocument()
  })
})

describe('Deferred reason filter (G4)', () => {
  it('offers exactly the six stored reasons and All reasons, with no Other', async () => {
    await openDeferred()
    const options = within(screen.getByLabelText('Reason')).getAllByRole('option')
    expect(options.map((o) => o.getAttribute('value'))).toEqual(['', ...deferredReasons])
    expect(options.map((o) => o.textContent)).toEqual([
      'All reasons',
      'Re-queued by user',
      'Quota exhausted',
      'Waiting for YouTube access',
      'Stopped by user',
      'Discarded by user',
      'Rate limited by YouTube',
    ])
  })

  it('applies only on Apply filter, resets to page 1, and keeps the whole-queue tab count', async () => {
    const spy = vi.spyOn(globalThis, 'fetch')
    await openDeferred('?tab=deferred&after_id=50')
    await screen.findByText('Showing 51–100 of 120 items.')

    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'quota_exhausted' } })
    // Choosing an option does not filter.
    expect(where()).toBe('?tab=deferred&after_id=50')
    expect(screen.getByText('Showing 51–100 of 120 items.')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Apply filter' }))
    await screen.findByText('Showing 1–3 of 3 items with this reason. 120 items in the deferred queue.')
    expect(where()).toBe('?tab=deferred&reason=quota_exhausted')
    // The server filtered: the request carried the reason and no page was filtered in the browser.
    expect(spy.mock.calls.map((c) => String(c[0]))).toContain('/api/deferred?reason=quota_exhausted&limit=50')
    expect(screen.getByRole('tab', { name: 'Deferred (120)' })).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: /^Dismiss deferred item/ })).toHaveLength(3)

    fireEvent.click(screen.getByRole('button', { name: 'Clear filter' }))
    await screen.findByText('Showing 1–50 of 120 items.')
    expect(where()).toBe('?tab=deferred')
    expect(screen.getByLabelText('Reason')).toHaveValue('')
  })

  it('says so when no item has the reason', async () => {
    await openDeferred()
    fireEvent.change(screen.getByLabelText('Reason'), { target: { value: 'stopped_by_user' } })
    fireEvent.click(screen.getByRole('button', { name: 'Apply filter' }))
    expect(await screen.findByText('No deferred items have this reason.')).toBeInTheDocument()
    expect(screen.queryByText('No deferred work. All pending items have been processed.')).not.toBeInTheDocument()
  })

  it('ignores an unknown reason in the URL, says so, and shows all reasons', async () => {
    const spy = vi.spyOn(globalThis, 'fetch')
    await openDeferred('?tab=deferred&reason=bogus')
    expect(await screen.findByText('Unknown reason filter. Showing all reasons.')).toBeInTheDocument()
    expect(screen.getByText('Showing 1–50 of 120 items.')).toBeInTheDocument()
    expect(screen.getByLabelText('Reason')).toHaveValue('')
    expect(spy.mock.calls.map((c) => String(c[0])).filter((u) => u.includes('bogus'))).toEqual([])
  })
})

describe('Deferred rows and Dismiss (G4)', () => {
  it('labels stored reasons, never echoes an unknown one, and marks a re-queued row not eligible yet', async () => {
    const future = new Date(Date.now() + 3600_000).toISOString()
    mock.resetMockDeferred([
      item(1, 'requeued_by_user'),
      item(2, 'requeued_by_user', { retry_after: future }),
      item(3, 'api_rate_limited'),
      item(4, 'some_future_reason'),
      item(5, 'quota_exhausted', { retry_after: future }),
      item(6, 'authorization_pending', { video_title: undefined }),
    ])
    await openDeferred()
    const row = (title: string) => screen.getByText(title).closest('tr')!

    expect(within(row('Video 1')).getByText('Re-queued by user')).toBeInTheDocument()
    expect(within(row('Video 1')).getByText('Eligible for the next run')).toBeInTheDocument()
    expect(within(row('Video 1')).queryByText(/Not eligible yet/)).not.toBeInTheDocument()

    expect(within(row('Video 2')).getByText('Re-queued by user Not eligible yet.')).toBeInTheDocument()
    expect(within(row('Video 2')).queryByText('Eligible for the next run')).not.toBeInTheDocument()

    expect(within(row('Video 3')).getByText('Rate limited by YouTube')).toBeInTheDocument()
    expect(within(row('Video 4')).getByText('Waiting for the next run.')).toBeInTheDocument()
    expect(screen.queryByText(/some_future_reason/)).not.toBeInTheDocument()
    // Only a re-queued row carries the extra "Not eligible yet."
    expect(within(row('Video 5')).getByText('Quota exhausted')).toBeInTheDocument()

    expect(screen.getByText(/Video title unavailable/)).toBeInTheDocument()
    expect(screen.getByText('Video ID: 1006')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Dismiss deferred item: 1006' })).toBeInTheDocument()
  })

  it('reloads the same position after Dismiss and moves focus to the row that took its place', async () => {
    const spy = vi.spyOn(globalThis, 'fetch')
    await openDeferred('?tab=deferred&after_id=50')
    await screen.findByText('Showing 51–100 of 120 items.')

    fireEvent.click(screen.getByRole('button', { name: 'Dismiss deferred item: Video 52' }))
    await screen.findByText('Showing 51–100 of 119 items.')
    expect(screen.queryByText('Video 52')).not.toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Deferred (119)' })).toBeInTheDocument()
    expect(where()).toBe('?tab=deferred&after_id=50')
    const reload = spy.mock.calls.map((c) => String(c[0])).filter((u) => u === '/api/deferred?limit=50&after_id=50')
    expect(reload).toHaveLength(2)
    await waitFor(() => expect(screen.getByText('Video 53').closest('tr')).toHaveFocus())
  })

  it('goes to the last page when dismissing the only item on the last page', async () => {
    mock.resetMockDeferred(seed(51))
    await openDeferred('?tab=deferred&after_id=50')
    await screen.findByText('Showing 51–51 of 51 items.')
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss deferred item: Video 51' }))
    await screen.findByText('Showing 1–50 of 50 items.')
    expect(where()).toBe('?tab=deferred&page=last')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Deferred queue' })).toHaveFocus())
    expect(screen.getByRole('tab', { name: 'Deferred (50)' })).toBeInTheDocument()
  })
})

describe('Deferred states (G4)', () => {
  it('shows the empty state when the queue is empty', async () => {
    mock.resetMockDeferred([])
    renderAt('?tab=deferred')
    expect(await screen.findByText('No deferred work. All pending items have been processed.')).toBeInTheDocument()
    expect(screen.getByRole('tab', { name: 'Deferred (0)' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Deferred queue pages' })).not.toBeInTheDocument()
  })

  it('shows a loading state while the page is read', async () => {
    renderAt('?tab=deferred')
    expect(await screen.findByText('Loading deferred items…')).toBeInTheDocument()
    await screen.findByText('Showing 1–50 of 120 items.')
  })

  it('shows a read failure with Retry that recovers, and changes nothing', async () => {
    const real = globalThis.fetch
    let fail = true
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init) => {
      if (String(input).includes('/api/deferred') && fail) return new Response('{}', { status: 500 })
      return real(input, init)
    })
    renderAt('?tab=deferred')
    expect(await screen.findByText('Could not load the deferred queue. Nothing was changed.')).toBeInTheDocument()
    fail = false
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByText('Showing 1–50 of 120 items.')).toBeInTheDocument()
  })

  it('says the page no longer exists when it is past the end, and offers the last page', async () => {
    await openDeferred('?tab=deferred&page=9')
    expect(await screen.findByText('This page no longer exists. The queue has changed.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Go to the last page' }))
    await screen.findByText('Showing 101–120 of 120 items.')
    expect(where()).toBe('?tab=deferred&page=last')
  })
})
