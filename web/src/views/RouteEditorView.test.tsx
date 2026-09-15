import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RouteEditorView } from './RouteEditorView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Route as RouteModel } from '../types'

const routeFixture = (overrides: Partial<RouteModel> = {}): RouteModel => ({
  id: 1,
  name: 'Music Discovery',
  description: 'Route music-related uploads to the discovery playlist.',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [
    { id: 101, route_id: 1, condition_index: 0, field: 'title', operator: 'contains', value: 'music' },
  ],
  actions: [
    { id: 201, route_id: 1, action_type: 'playlist_insert', target_playlist_id: 'PLmusic', action_index: 0 },
  ],
  exclusions: [],
  created_at: '2026-08-01T10:00:00Z',
  updated_at: '2026-09-08T10:00:00Z',
  ...overrides,
})

function renderWithToast(ui: React.ReactElement) {
  return render(<ToastProvider>{ui}</ToastProvider>)
}

function renderAt(path: string) {
  return renderWithToast(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/routes" element={<div data-testid="routes-list">Routes list</div>} />
        <Route path="/routes/new" element={<RouteEditorView />} />
        <Route path="/routes/:id" element={<RouteEditorView />} />
      </Routes>
    </MemoryRouter>
  )
}

describe('RouteEditorView', () => {
  beforeEach(() => {
    window.history.replaceState({}, '', '/')
    vi.clearAllMocks()
  })

  it('creates a new route with the next evaluation order', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([
      routeFixture({ id: 1, name: 'A', evaluation_order: 1 }),
      routeFixture({ id: 2, name: 'B', evaluation_order: 2 }),
    ])
    const createSpy = vi.spyOn(mock, 'createRoute').mockResolvedValueOnce(routeFixture({ id: 3, evaluation_order: 3 }))

    renderAt('/routes/new')

    await waitFor(() => expect(screen.queryByText('Loading route...')).not.toBeInTheDocument())

    fireEvent.change(screen.getByLabelText(/Route name/i), { target: { value: 'New Test Route' } })

    fireEvent.click(screen.getByRole('button', { name: /Add condition/i }))
    fireEvent.change(screen.getByLabelText(/Condition 1 value/i), { target: { value: 'test' } })

    fireEvent.click(screen.getByRole('button', { name: /Add playlist action/i }))
    fireEvent.change(screen.getByLabelText(/Action 1 target playlist/i), { target: { value: 'PLnew' } })

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(createSpy).toHaveBeenCalled())

    const submitted = createSpy.mock.calls[0][0]
    expect(submitted.name).toBe('New Test Route')
    expect(submitted.evaluation_order).toBe(3)
    expect(submitted.conditions).toHaveLength(1)
    expect(submitted.actions).toHaveLength(1)

    await waitFor(() => expect(screen.getByTestId('routes-list')).toBeInTheDocument())
  })

  it('shows an error toast when route creation fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([])
    vi.spyOn(mock, 'createRoute').mockRejectedValueOnce(new Error('duplicate evaluation order'))

    renderAt('/routes/new')

    await waitFor(() => expect(screen.queryByText('Loading route...')).not.toBeInTheDocument())

    fireEvent.change(screen.getByLabelText(/Route name/i), { target: { value: 'Failing Route' } })
    fireEvent.click(screen.getByRole('button', { name: /Add condition/i }))
    fireEvent.change(screen.getByLabelText(/Condition 1 value/i), { target: { value: 'x' } })
    fireEvent.click(screen.getByRole('button', { name: /Add playlist action/i }))
    fireEvent.change(screen.getByLabelText(/Action 1 target playlist/i), { target: { value: 'PLx' } })
    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/duplicate evaluation order/i))
    expect(screen.queryByTestId('routes-list')).not.toBeInTheDocument()
  })

  it('edits an existing route and calls the update endpoint', async () => {
    vi.spyOn(mock, 'getRoute').mockResolvedValueOnce(routeFixture())
    const updateSpy = vi.spyOn(mock, 'updateRoute').mockResolvedValueOnce(routeFixture({ name: 'Updated Music' }))

    renderAt('/routes/1')

    await waitFor(() => expect(screen.getByDisplayValue('Music Discovery')).toBeInTheDocument())

    fireEvent.change(screen.getByLabelText(/Route name/i), { target: { value: 'Updated Music' } })
    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(updateSpy).toHaveBeenCalled())

    const [routeId, submitted] = updateSpy.mock.calls[0]
    expect(routeId).toBe(1)
    expect(submitted.name).toBe('Updated Music')

    await waitFor(() => expect(screen.getByTestId('routes-list')).toBeInTheDocument())
  })

  it('deletes an existing route and calls the delete endpoint', async () => {
    vi.spyOn(mock, 'getRoute').mockResolvedValueOnce(routeFixture())
    const deleteSpy = vi.spyOn(mock, 'deleteRoute').mockResolvedValueOnce(undefined)

    renderAt('/routes/1')

    await waitFor(() => expect(screen.getByDisplayValue('Music Discovery')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Delete/i }))

    await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument())
    fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: /^Delete$/i }))

    await waitFor(() => expect(deleteSpy).toHaveBeenCalledWith(1))
    await waitFor(() => expect(screen.getByTestId('routes-list')).toBeInTheDocument())
  })

  it('shows route not found for an unknown id', async () => {
    vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce([routeFixture()])
    vi.spyOn(mock, 'getRoute').mockResolvedValueOnce(undefined)

    renderAt('/routes/999')

    await waitFor(() => expect(screen.queryByText('Loading route...')).not.toBeInTheDocument())
    expect(screen.getByText('Route not found')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: /Back to Routes/i })).toBeInTheDocument()
  })

  it('shows a network error with retry', async () => {
    vi.spyOn(mock, 'listRoutes').mockRejectedValueOnce(new Error('network error'))

    renderAt('/routes/1')

    await waitFor(() => expect(screen.queryByText('Loading route...')).not.toBeInTheDocument())
    expect(screen.getByText('Failed to load route')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Retry/i })).toBeInTheDocument()
  })
})
