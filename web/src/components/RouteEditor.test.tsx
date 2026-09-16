import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { RouteEditor } from './RouteEditor'
import * as mock from '../api/mock'
import type { Route } from '../types'

const routeFixture = (overrides: Partial<Route> = {}): Route => ({
  id: 1,
  name: 'Music Discovery',
  description: '',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [{ id: 101, route_id: 1, condition_index: 0, field: 'title', operator: 'contains', value: 'music' }],
  actions: [{ id: 201, route_id: 1, action_type: 'playlist_insert', target_playlist_id: 'PLmusic', action_index: 0 }],
  exclusions: [],
  created_at: '2026-08-01T10:00:00Z',
  updated_at: '2026-09-08T10:00:00Z',
  ...overrides,
})

describe('RouteEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.spyOn(mock, 'listPlaylists').mockResolvedValue([
      { playlist_id: 'PLmusic', playlist_name: 'Music Discovery' },
      { playlist_id: 'PLambient', playlist_name: 'Ambient' },
    ])
  })

  it('shows validation errors for empty name, conditions, and actions', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({ name: '', conditions: [], actions: [] })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(screen.getByText('Route name is required')).toBeInTheDocument())
    expect(screen.getByText('Add at least one condition')).toBeInTheDocument()
    expect(screen.getByText('Add at least one action')).toBeInTheDocument()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('shows validation error when name exceeds 100 characters', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({ name: 'a'.repeat(101), conditions: [], actions: [] })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() =>
      expect(screen.getByText('Route name must be 100 characters or fewer')).toBeInTheDocument()
    )
    expect(onSave).not.toHaveBeenCalled()
  })

  it('shows validation error when name is not unique', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({ id: 2, name: 'Ambient' })}
        existingRoutes={[routeFixture({ id: 1, name: 'Ambient' })]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() =>
      expect(screen.getByText('A route with this name already exists')).toBeInTheDocument()
    )
    expect(onSave).not.toHaveBeenCalled()
  })

  it('shows per-condition value validation errors', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          conditions: [{ id: 101, route_id: 1, condition_index: 0, field: 'title', operator: 'contains', value: '' }],
        })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(screen.getByText('Enter a value')).toBeInTheDocument())
    expect(onSave).not.toHaveBeenCalled()
  })

  it('shows per-action playlist validation errors', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          actions: [{ id: 201, route_id: 1, action_type: 'playlist_insert', target_playlist_id: '', action_index: 0 }],
        })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(screen.getByText('Select a playlist')).toBeInTheDocument())
    expect(onSave).not.toHaveBeenCalled()
  })

  it('captures the first keystroke in duration min input', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({ exclusions: [] })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    const minInput = screen.getByLabelText(/Min duration/i)
    fireEvent.change(minInput, { target: { value: '300' } })

    await waitFor(() => expect(minInput).toHaveValue(300))

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))
    await waitFor(() => expect(onSave).toHaveBeenCalled())

    const submitted = onSave.mock.calls[0][0] as Route
    expect(submitted.exclusions).toContainEqual(
      expect.objectContaining({ exclusion_type: 'duration_min', exclusion_value: '300' })
    )
  })

  it('captures the first keystroke in duration max input', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({ exclusions: [] })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    const maxInput = screen.getByLabelText(/Max duration/i)
    fireEvent.change(maxInput, { target: { value: '600' } })

    await waitFor(() => expect(maxInput).toHaveValue(600))

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))
    await waitFor(() => expect(onSave).toHaveBeenCalled())

    const submitted = onSave.mock.calls[0][0] as Route
    expect(submitted.exclusions).toContainEqual(
      expect.objectContaining({ exclusion_type: 'duration_max', exclusion_value: '600' })
    )
  })

  it('opens delete confirmation dialog and calls onDelete on confirm', async () => {
    const onDelete = vi.fn()
    render(
      <RouteEditor
        route={routeFixture()}
        existingRoutes={[]}
        onSave={() => {}}
        onCancel={() => {}}
        onDelete={onDelete}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Delete/i }))

    await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument())
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Delete route 'Music Discovery'?/)).toBeInTheDocument()

    fireEvent.click(within(dialog).getByRole('button', { name: /^Delete$/i }))

    await waitFor(() => expect(onDelete).toHaveBeenCalled())
  })

  it('cancels delete confirmation dialog', async () => {
    const onDelete = vi.fn()
    render(
      <RouteEditor
        route={routeFixture()}
        existingRoutes={[]}
        onSave={() => {}}
        onCancel={() => {}}
        onDelete={onDelete}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Delete/i }))

    await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument())

    fireEvent.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: /^Cancel$/i }))

    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(onDelete).not.toHaveBeenCalled()
  })
})
