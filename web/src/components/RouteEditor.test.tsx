import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, within } from '@testing-library/react'
import { RouteEditor } from './RouteEditor'
import * as mock from '../api/mock'
import type { Route } from '../types'

const ROUTE_UUID = '11111111-2222-4333-8444-555555555555'

const routeFixture = (overrides: Partial<Route> = {}): Route => ({
  id: ROUTE_UUID,
  name: 'Music Discovery',
  description: '',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [{ id: 101, route_id: ROUTE_UUID, condition_index: 0, field: 'title', operator: 'contains', value: 'music' }],
  actions: [{ id: 201, route_id: ROUTE_UUID, action_type: 'add_to_playlist', target_playlist_id: 'PLmusic', action_index: 0 }],
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

  it('accepts a duplicate display name (identity is the UUID, not the name)', async () => {
    const onSave = vi.fn()
    const duplicateUuid = '99999999-8888-4777-a666-555555555555'
    render(
      <RouteEditor
        route={routeFixture({ id: duplicateUuid, name: 'Ambient' })}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    // BR-003 AC10 (amended): a name matching another route is not an error.
    await waitFor(() => expect(onSave).toHaveBeenCalled())
    expect(screen.queryByText('A route with this name already exists')).not.toBeInTheDocument()
  })

  it('shows per-condition value validation errors', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          conditions: [{ id: 101, route_id: ROUTE_UUID, condition_index: 0, field: 'title', operator: 'contains', value: '' }],
        })}
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
          actions: [{ id: 201, route_id: ROUTE_UUID, action_type: 'add_to_playlist', target_playlist_id: '', action_index: 0 }],
        })}
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
        onSave={() => {}}
        onCancel={() => {}}
        onDelete={onDelete}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Delete/i }))

    await waitFor(() => expect(screen.getByRole('alertdialog')).toBeInTheDocument())
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText(/Delete route 'Music Discovery \(11111111\)'?/)).toBeInTheDocument()

    fireEvent.click(within(dialog).getByRole('button', { name: /^Delete$/i }))

    await waitFor(() => expect(onDelete).toHaveBeenCalled())
  })

  it('cancels delete confirmation dialog', async () => {
    const onDelete = vi.fn()
    render(
      <RouteEditor
        route={routeFixture()}
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
          conditions: [{ id: 101, route_id: ROUTE_UUID, condition_index: 0, field: 'duration_seconds', operator: 'between', value: '180,300' }],
        })}
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
          conditions: [{ id: 101, route_id: ROUTE_UUID, condition_index: 0, field: 'duration_seconds', operator: 'greater_than', value: 'abc' }],
        })}
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
              route_id: ROUTE_UUID,
              condition_index: 0,
              field: 'duration_seconds',
              operator: 'between',
              value: '180,',
            },
          ],
        })}
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

  it('moves focus to Cancel on open, traps Tab inside the delete dialog, and restores focus on Escape', async () => {
    const onDelete = vi.fn()
    render(
      <RouteEditor
        route={routeFixture()}
        onSave={() => {}}
        onCancel={() => {}}
        onDelete={onDelete}
      />
    )

    const deleteButton = screen.getByRole('button', { name: /^Delete$/ })
    deleteButton.focus()
    fireEvent.click(deleteButton)

    const dialog = await waitFor(() => screen.getByRole('alertdialog'))
    const cancel = within(dialog).getByRole('button', { name: /^Cancel$/ })
    const confirm = within(dialog).getByRole('button', { name: /^Delete$/ })
    await waitFor(() => expect(cancel).toHaveFocus())

    // Shift+Tab from the first control wraps to the last one instead of
    // escaping into the page behind the dialog.
    fireEvent.keyDown(cancel, { key: 'Tab', shiftKey: true })
    expect(confirm).toHaveFocus()
    // Tab from the last control wraps back to the first.
    fireEvent.keyDown(confirm, { key: 'Tab' })
    expect(cancel).toHaveFocus()

    fireEvent.keyDown(document, { key: 'Escape' })
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(deleteButton).toHaveFocus()
    expect(onDelete).not.toHaveBeenCalled()
  })

  it('asks before discarding unsaved changes and keeps editing when asked to', async () => {
    const onCancel = vi.fn()
    render(<RouteEditor route={routeFixture()} onSave={() => {}} onCancel={onCancel} />)

    fireEvent.change(screen.getByLabelText(/Route name/i), { target: { value: 'Renamed route' } })
    const cancelButton = screen.getByRole('button', { name: /^Cancel$/ })
    cancelButton.focus()
    fireEvent.click(cancelButton)

    const dialog = await waitFor(() => screen.getByRole('alertdialog'))
    expect(within(dialog).getByText('Discard unsaved changes?')).toBeInTheDocument()
    const keepEditing = within(dialog).getByRole('button', { name: /Keep editing/i })
    await waitFor(() => expect(keepEditing).toHaveFocus())

    fireEvent.click(keepEditing)

    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(onCancel).not.toHaveBeenCalled()
    expect(cancelButton).toHaveFocus()
  })

  it('leaves the editor when unsaved changes are discarded', async () => {
    const onCancel = vi.fn()
    render(<RouteEditor route={routeFixture()} onSave={() => {}} onCancel={onCancel} />)

    fireEvent.change(screen.getByLabelText(/Route name/i), { target: { value: 'Renamed route' } })
    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }))

    const dialog = await waitFor(() => screen.getByRole('alertdialog'))
    fireEvent.click(within(dialog).getByRole('button', { name: /Discard/i }))

    await waitFor(() => expect(onCancel).toHaveBeenCalled())
  })

  it('cancels immediately when there are no unsaved changes', async () => {
    const onCancel = vi.fn()
    render(<RouteEditor route={routeFixture()} onSave={() => {}} onCancel={onCancel} />)

    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }))

    expect(onCancel).toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  })

  it('shows the v1 action type selector with the playlist action selected', async () => {
    render(<RouteEditor route={routeFixture()} onSave={() => {}} onCancel={() => {}} />)

    const typeSelect = await waitFor(() => screen.getByLabelText('Action type'))
    expect(typeSelect).toHaveValue('add_to_playlist')
    // BR-004 AC8: v1 offers exactly one action type.
    expect(within(typeSelect).getAllByRole('option')).toHaveLength(1)
  })

  it('rejects saving an action whose type is not supported', async () => {
    const onSave = vi.fn()
    render(
      <RouteEditor
        route={routeFixture({
          actions: [
            {
              id: 201,
              route_id: ROUTE_UUID,
              action_type: 'unsupported' as never,
              target_playlist_id: 'PLmusic',
              action_index: 0,
            },
          ],
        })}
        onSave={onSave}
        onCancel={() => {}}
      />
    )

    fireEvent.click(screen.getByRole('button', { name: /Save route/i }))

    await waitFor(() => expect(screen.getByText('Select a supported action type')).toBeInTheDocument())
    expect(onSave).not.toHaveBeenCalled()
  })

  it('appends a new action row that defaults to the playlist action type', async () => {
    render(<RouteEditor route={routeFixture()} onSave={() => {}} onCancel={() => {}} />)

    fireEvent.click(screen.getByRole('button', { name: /Add playlist action/i }))

    const typeSelects = screen.getAllByLabelText('Action type')
    expect(typeSelects).toHaveLength(2)
    expect(typeSelects[1]).toHaveValue('add_to_playlist')
  })

  it('shows the full UUID and timestamps with a copy control in edit mode', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    render(<RouteEditor route={routeFixture()} onSave={() => {}} onCancel={() => {}} />)

    // The full canonical UUID (not only the short suffix) is visible and copyable.
    expect(screen.getByText(ROUTE_UUID)).toBeInTheDocument()
    expect(screen.getByText(new Date('2026-08-01T10:00:00Z').toLocaleString(), { exact: false })).toBeInTheDocument()
    expect(screen.getByText(new Date('2026-09-08T10:00:00Z').toLocaleString(), { exact: false })).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Copy route ID/i }))

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(ROUTE_UUID))
    await waitFor(() => expect(screen.getByText('Route ID copied')).toBeInTheDocument())
  })

  it('hides the route ID metadata for an unsaved new route', () => {
    render(<RouteEditor onSave={() => {}} onCancel={() => {}} />)

    expect(screen.queryByText('Route ID')).not.toBeInTheDocument()
  })
})
