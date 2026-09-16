import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RoutesView } from './RoutesView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Route as RouteModel, RouteCondition } from '../types'

const routeFixture = (overrides: Partial<RouteModel> = {}): RouteModel => ({
  id: 1,
  name: 'Music Discovery',
  description: '',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [
    { id: 101, route_id: 1, condition_index: 0, field: 'title', operator: 'contains', value: 'music' } as RouteCondition,
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
      routeFixture({ id: 2, name: 'Ambient', evaluation_order: 2 }),
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

  it('reverts order and shows a toast when reorder fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture(),
      routeFixture({ id: 2, name: 'Ambient', evaluation_order: 2 }),
    ])
    vi.spyOn(mock, 'reorderRoutes').mockRejectedValueOnce(new Error('reorder failed'))

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    const rows = screen.getAllByRole('row')
    const firstRow = rows[1]
    fireEvent.click(firstRow.querySelector('[aria-label="Move Music Discovery down"]') as HTMLElement)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/reorder failed/i))
    expect(screen.getAllByRole('row')[1]).toHaveTextContent('Music Discovery')
  })

  it('does not corrupt evaluation_order after a failed reorder', async () => {
    const updateRoute = vi.spyOn(mock, 'updateRoute').mockResolvedValueOnce(routeFixture({ is_enabled: false }))
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture(),
      routeFixture({ id: 2, name: 'Ambient', evaluation_order: 2 }),
    ])
    vi.spyOn(mock, 'reorderRoutes').mockRejectedValueOnce(new Error('reorder failed'))

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    const rows = screen.getAllByRole('row')
    fireEvent.click(rows[1].querySelector('[aria-label="Move Music Discovery down"]') as HTMLElement)
    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/reorder failed/i))

    fireEvent.click(screen.getByRole('switch', { name: /Disable route Music Discovery/i }))

    await waitFor(() => expect(updateRoute).toHaveBeenCalled())
    const payload = updateRoute.mock.calls[0][1] as RouteModel
    expect(payload.evaluation_order).toBe(1)
  })
})
