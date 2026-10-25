import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RoutesView } from './RoutesView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Route as RouteModel, RouteCondition } from '../types'

const ROUTE_A = '11111111-2222-4333-8444-555555555555'
const ROUTE_B = 'aaaabbbb-cccc-4ddd-8eee-ffff00000000'

const routeFixture = (overrides: Partial<RouteModel> = {}): RouteModel => ({
  id: ROUTE_A,
  name: 'Music Discovery',
  description: '',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [
    { id: 101, route_id: ROUTE_A, condition_index: 0, field: 'title', operator: 'contains', value: 'music' } as RouteCondition,
  ],
  actions: [],
  exclusions: [],
  match_count: 2,
  created_at: '2026-08-01T10:00:00Z',
  updated_at: '2026-09-08T10:00:00Z',
  ...overrides,
})

function renderView() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/routes']}>
        <Routes>
          <Route path="/routes" element={<RoutesView />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>
  )
}

describe('RoutesView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('renders condition summary, final badge, and match count', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture({ is_final: true }),
    ])

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())
    expect(screen.getByText(/title contains music/i)).toBeInTheDocument()
    expect(screen.getByText('Final', { selector: 'span.badge' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('2', { selector: '[data-label="Matches"]' })).toBeInTheDocument())
  })

  it('renders empty state when no routes exist', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([])

    renderView()

    const emptyState = await waitFor(() => screen.getByText(/No routes yet/i))
    expect(emptyState).toBeInTheDocument()
    expect(emptyState.closest('.empty-state')?.querySelector('a[href="/routes/new"]')).toBeInTheDocument()
  })

  it('rejects a null routes response as an error instead of normalizing it to empty', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce(null as unknown as RouteModel[])

    renderView()

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/Failed to load routes/i))
    expect(screen.queryByText(/No routes yet/i)).not.toBeInTheDocument()
  })

  it('shows the fetch error state with an accessible retry button when the list fetch fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockRejectedValueOnce(new Error('network down'))

    renderView()

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent('Failed to load routes. Check your connection and try again.')
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    expect(retry).toBeEnabled()
    await waitFor(() => expect(retry).toHaveFocus())

    // Toolbar stays visible but disabled; empty state is never presented.
    expect(screen.getByRole('button', { name: /New Route/i })).toBeDisabled()
    expect(screen.queryByText(/No routes yet/i)).not.toBeInTheDocument()
  })

  it('stays in the fetch error state and re-enables retry when retry also fails', async () => {
    vi.spyOn(mock, 'listRoutes')
      .mockRejectedValueOnce(new Error('network down'))
      .mockRejectedValueOnce(new Error('still down'))

    renderView()

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/Retry failed. Please try again./i))
    // The error region persists (disambiguated from the error toast, which is also role=alert).
    expect(screen.getByText(/Failed to load routes\. Check your connection and try again\./)).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled())
    expect(screen.queryByText(/No routes yet/i)).not.toBeInTheDocument()
  })

  it('recovers to the populated list after a successful retry', async () => {
    vi.spyOn(mock, 'listRoutes')
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce([routeFixture()])

    renderView()

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: /New Route/i })).toBeEnabled()
  })

  it('recovers to the empty state only when a retry succeeds with an empty list', async () => {
    vi.spyOn(mock, 'listRoutes')
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce([])

    renderView()

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText(/No routes yet/i)).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('shows a toast and reverts when toggling enabled fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([routeFixture()])
    vi.spyOn(mock, 'updateRoute').mockRejectedValueOnce(new Error('update failed'))

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('switch'))

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/update failed/i))
    expect(screen.getByRole('switch')).toBeChecked()
  })

  it('shows a toast when delete fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture(),
      routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2 }),
    ])
    vi.spyOn(mock, 'deleteRoute').mockRejectedValueOnce(new Error('delete failed'))

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Delete route Music Discovery/i }))

    const dialog = await waitFor(() => screen.getByRole('alertdialog'))
    fireEvent.click(within(dialog).getByRole('button', { name: /^Delete$/i }))

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/delete failed/i))
    expect(screen.getByText('Music Discovery')).toBeInTheDocument()
  })

  it('confirms deletion in an accessible dialog and moves focus to the next row', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture(),
      routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2 }),
    ])
    const deleteRoute = vi.spyOn(mock, 'deleteRoute').mockResolvedValueOnce(undefined)

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    const trigger = screen.getByRole('button', { name: /Delete route Music Discovery/i })
    trigger.focus()
    fireEvent.click(trigger)

    const dialog = await waitFor(() => screen.getByRole('alertdialog'))
    await waitFor(() => expect(within(dialog).getByRole('button', { name: /^Cancel$/i })).toHaveFocus())

    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(trigger).toHaveFocus()

    fireEvent.click(trigger)
    const reopened = await waitFor(() => screen.getByRole('alertdialog'))
    fireEvent.click(within(reopened).getByRole('button', { name: /^Delete$/i }))

    await waitFor(() => expect(deleteRoute).toHaveBeenCalledWith(ROUTE_A))
    await waitFor(() => expect(screen.queryByText('Music Discovery')).not.toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Ambient').closest('tr')).toHaveFocus())
  })

  it('reverts order and shows a toast when reorder fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture(),
      routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2 }),
    ])
    vi.spyOn(mock, 'reorderRoutes').mockRejectedValueOnce(new Error('reorder failed'))

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    const rows = screen.getAllByRole('row')
    const firstRow = rows[1]
    fireEvent.click(firstRow.querySelector('[aria-label="Move Music Discovery (11111111) down"]') as HTMLElement)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/reorder failed/i))
    expect(screen.getAllByRole('row')[1]).toHaveTextContent('Music Discovery')
  })

  it('does not corrupt evaluation_order after a failed reorder', async () => {
    const updateRoute = vi.spyOn(mock, 'updateRoute').mockResolvedValueOnce(routeFixture({ is_enabled: false }))
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture(),
      routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2 }),
    ])
    vi.spyOn(mock, 'reorderRoutes').mockRejectedValueOnce(new Error('reorder failed'))

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    const rows = screen.getAllByRole('row')
    fireEvent.click(rows[1].querySelector('[aria-label="Move Music Discovery (11111111) down"]') as HTMLElement)
    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/reorder failed/i))

    fireEvent.click(screen.getByRole('switch', { name: /Disable route Music Discovery/i }))

    await waitFor(() => expect(updateRoute).toHaveBeenCalled())
    const payload = updateRoute.mock.calls[0][1] as RouteModel
    expect(payload.evaluation_order).toBe(1)
  })

  it('shows distinct short UUID suffixes and per-route match counts for same-named routes', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture({ name: 'Ambient', match_count: 2 }),
      routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2, match_count: 5 }),
    ])

    renderView()

    await waitFor(() =>
      expect(screen.getByRole('link', { name: /Ambient \(11111111\)/ })).toBeInTheDocument()
    )
    expect(screen.getByRole('link', { name: /Ambient \(aaaabbbb\)/ })).toBeInTheDocument()
    // Same-named routes keep their own rows and counts; nothing merges by name.
    const matches = Array.from(document.querySelectorAll('[data-label="Matches"]')).map((el) => el.textContent)
    expect(matches).toEqual(['2', '5'])
  })

  it('deletes one of two same-named routes by its own UUID and keeps the other', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture({ name: 'Ambient' }),
      routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2 }),
    ])
    const deleteRoute = vi.spyOn(mock, 'deleteRoute').mockResolvedValueOnce(undefined)

    renderView()

    await waitFor(() =>
      expect(screen.getByRole('link', { name: /Ambient \(11111111\)/ })).toBeInTheDocument()
    )
    fireEvent.click(screen.getByRole('button', { name: 'Delete route Ambient (11111111)' }))

    const dialog = await waitFor(() => screen.getByRole('alertdialog'))
    // The confirmation identifies which same-named route is being deleted.
    expect(dialog).toHaveTextContent("Delete route 'Ambient (11111111)'?")
    fireEvent.click(within(dialog).getByRole('button', { name: /^Delete$/i }))

    await waitFor(() => expect(deleteRoute).toHaveBeenCalledWith(ROUTE_A))
    await waitFor(() =>
      expect(screen.queryByRole('link', { name: /Ambient \(11111111\)/ })).not.toBeInTheDocument()
    )
    expect(screen.getByRole('link', { name: /Ambient \(aaaabbbb\)/ })).toBeInTheDocument()
  })

  it('reorders same-named routes by UUID, not by name', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture({ name: 'Ambient' }),
      routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2 }),
    ])
    const reorder = vi.spyOn(mock, 'reorderRoutes').mockResolvedValueOnce(undefined)

    renderView()

    await waitFor(() =>
      expect(screen.getByRole('button', { name: 'Move Ambient (11111111) down' })).toBeInTheDocument()
    )
    fireEvent.click(screen.getByRole('button', { name: 'Move Ambient (11111111) down' }))

    await waitFor(() => expect(reorder).toHaveBeenCalledWith([ROUTE_B, ROUTE_A]))
  })
})
