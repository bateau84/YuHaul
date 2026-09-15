import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RoutesView } from './RoutesView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Route as RouteModel } from '../types'

const routeFixture = (overrides: Partial<RouteModel> = {}): RouteModel => ({
  id: 1,
  name: 'Music Discovery',
  description: '',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [],
  actions: [],
  exclusions: [],
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

  it('shows a toast when delete fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture(),
      routeFixture({ id: 2, name: 'Ambient', evaluation_order: 2 }),
    ])
    vi.spyOn(mock, 'deleteRoute').mockRejectedValueOnce(new Error('delete failed'))

    renderView()

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Delete route Music Discovery/i }))

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
})
