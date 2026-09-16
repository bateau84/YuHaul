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

  it('tolerates null nested arrays from the API and treats them as empty', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          name: '',
          conditions: null as unknown as RouteCondition[],
          actions: null as unknown as RouteAction[],
          exclusions: null as unknown as RouteExclusion[],
        })}
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

  it('shows empty playlist state with link to YouTube', async () => {
    vi.spyOn(mock, 'listPlaylists').mockResolvedValue([])
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture()}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    await waitFor(() => expect(screen.getByText('No playlists found.')).toBeInTheDocument())
    expect(screen.getByRole('link', { name: /Create a playlist on YouTube first/i })).toHaveAttribute('href', 'https://www.youtube.com')
  })

  it('shows playlist error with retry button', async () => {
    vi.spyOn(mock, 'listPlaylists').mockRejectedValueOnce(new Error('network error'))
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture()}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(/network error/i))
    fireEvent.click(screen.getByRole('button', { name: /Retry/i }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })

  it('cleans the between value when switching to a single-value operator', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          conditions: [{ id: 101, route_id: 1, condition_index: 0, field: 'duration_seconds', operator: 'between', value: '180,300' }],
        })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.change(screen.getByLabelText(/Condition 1 operator/i), { target: { value: 'greater_than' } })

    const valueInput = screen.getByLabelText(/Condition 1 value/i) as HTMLInputElement
    await waitFor(() => expect(valueInput.value).toBe('180'))

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))
    await waitFor(() => expect(onSave).toHaveBeenCalled())

    const submitted = onSave.mock.calls[0][0] as Route
    expect(submitted.conditions[0].value).toBe('180')
  })

  it('validates numeric conditions', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          conditions: [{ id: 101, route_id: 1, condition_index: 0, field: 'duration_seconds', operator: 'greater_than', value: 'abc' }],
        })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(screen.getByText('Enter a number')).toBeInTheDocument())
    expect(onSave).not.toHaveBeenCalled()
  })

  it('shows validation error for between operator with a missing bound', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          conditions: [
            {
              id: 101,
              route_id: 1,
              condition_index: 0,
              field: 'duration_seconds',
              operator: 'between',
              value: '180,',
            },
          ],
        })}
        existingRoutes={[]}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() =>
      expect(screen.getByText('Enter two numbers separated by a comma')).toBeInTheDocument()
    )
    expect(onSave).not.toHaveBeenCalled()
  })
})
