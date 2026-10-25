import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { BacktestView } from './BacktestView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Route } from '../types'

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <ToastProvider>{ui}</ToastProvider>
    </MemoryRouter>
  )
}

function routeFixture(overrides: Partial<Route>): Route {
  return {
    id: '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce',
    name: 'Music Discovery',
    description: '',
    evaluation_order: 1,
    is_final: false,
    is_enabled: true,
    condition_composition: 'AND',
    conditions: [{ condition_index: 0, field: 'title', operator: 'contains', value: 'music' }],
    actions: [{ action_type: 'add_to_playlist', target_playlist_id: 'PL1', action_index: 0 }],
    exclusions: [],
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...overrides,
  }
}

function uploadRoutes(routes: Route[]) {
  fireEvent.click(screen.getByRole('tab', { name: /Upload config file/i }))
  const file = new File([JSON.stringify({ routes })], 'proposed.json', { type: 'application/json' })
  fireEvent.change(screen.getByLabelText(/Upload configuration file/i), { target: { files: [file] } })
}

describe('BacktestView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('supports config-based hypothetical backtest with comparison results', async () => {
    renderWithProviders(<BacktestView />)

    await waitFor(() => expect(screen.getByRole('heading', { name: /Backtest & Replay/i })).toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Run backtest/i }))

    await waitFor(() => expect(screen.getByText(/Comparison results/i)).toBeInTheDocument())
    expect(screen.getByText('Historical accepted')).toBeInTheDocument()
    expect(screen.getByText('Proposed accepted')).toBeInTheDocument()
    expect(screen.getByText('New catches')).toBeInTheDocument()
  })

  it('supports file upload for proposed configuration', async () => {
    renderWithProviders(<BacktestView />)

    await waitFor(() => expect(screen.getByText('Proposed configuration')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('tab', { name: /Upload config file/i }))

    const file = new File(
      [JSON.stringify({ routes: [{ name: 'Uploaded Route', id: '0b7f6d2e-1c3a-4d5b-8e6f-2a4c6d8e0f1b', evaluation_order: 1, is_final: false, is_enabled: true, condition_composition: 'AND', conditions: [], actions: [], exclusions: [], created_at: '', updated_at: '' }] })],
      'proposed.json',
      { type: 'application/json' }
    )
    const input = screen.getByLabelText(/Upload configuration file/i) as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })

    await waitFor(() => expect(screen.getAllByText(/Loaded 1 proposed routes from file/i)).toHaveLength(2))
  })

  it('displays scenario diff between current and proposed routes', async () => {
    renderWithProviders(<BacktestView />)

    await waitFor(() => expect(screen.getByText('Scenario diff')).toBeInTheDocument())
    expect(screen.getByText(/Proposed configuration matches the current active configuration/i)).toBeInTheDocument()
  })

  it('supports replay mode with configuration version selector', async () => {
    renderWithProviders(<BacktestView />)

    await waitFor(() => expect(screen.getByRole('heading', { name: /Backtest & Replay/i })).toBeInTheDocument())
    fireEvent.click(screen.getByRole('tab', { name: /Replay\/Reprocess/i }))

    await waitFor(() => expect(screen.getByText('Select configuration version')).toBeInTheDocument())
    expect(screen.getByRole('combobox')).toBeInTheDocument()
  })

  it('shows error when uploaded file is invalid', async () => {
    renderWithProviders(<BacktestView />)

    await waitFor(() => expect(screen.getByText('Proposed configuration')).toBeInTheDocument())
    fireEvent.click(screen.getByRole('tab', { name: /Upload config file/i }))

    const file = new File(['not valid json'], 'bad.json', { type: 'application/json' })
    const input = screen.getByLabelText(/Upload configuration file/i) as HTMLInputElement
    fireEvent.change(input, { target: { files: [file] } })

    await waitFor(() => expect(screen.getByText(/not valid json/i)).toBeInTheDocument())
  })

  it('shows an accessible fetch-error state when route loading fails', async () => {
    vi.spyOn(mock, 'listRoutes').mockRejectedValueOnce(new Error('network down'))

    renderWithProviders(<BacktestView />)

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent('Failed to load routes. Check your connection and try again.')
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    await waitFor(() => expect(retry).toHaveFocus())

    // Never claim the proposal matches an unknown current configuration.
    expect(screen.queryByText(/Proposed configuration matches the current active configuration/i)).not.toBeInTheDocument()
    expect(screen.queryByText('Scenario diff')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: /Run backtest/i })).toBeDisabled()
  })

  it('recovers the route editor after a successful retry', async () => {
    vi.spyOn(mock, 'listRoutes').mockRejectedValueOnce(new Error('network down'))

    renderWithProviders(<BacktestView />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(await screen.findByText(/Proposed configuration matches the current active configuration/i)).toBeInTheDocument()
  })

  it('stays in the fetch-error state and re-enables retry when retry also fails', async () => {
    vi.spyOn(mock, 'listRoutes')
      .mockRejectedValueOnce(new Error('network down'))
      .mockRejectedValueOnce(new Error('still down'))

    renderWithProviders(<BacktestView />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/Retry failed. Please try again./i))
    expect(screen.getByText('Failed to load routes. Check your connection and try again.')).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled())
    expect(screen.queryByText(/Proposed configuration matches the current active configuration/i)).not.toBeInTheDocument()
  })

  it('shows an accessible fetch-error state when configuration versions fail to load in replay mode', async () => {
    vi.spyOn(mock, 'listConfigVersions').mockRejectedValueOnce(new Error('network down'))

    renderWithProviders(<BacktestView />)
    fireEvent.click(screen.getByRole('tab', { name: /Replay\/Reprocess/i }))

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent(
      'Failed to load configuration versions. Check your connection and try again.'
    )
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    // The selector is hidden rather than silently showing no versions.
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toHaveFocus())
  })

  it('recovers the configuration version selector after a successful retry', async () => {
    vi.spyOn(mock, 'listConfigVersions').mockRejectedValueOnce(new Error('network down'))

    renderWithProviders(<BacktestView />)
    fireEvent.click(screen.getByRole('tab', { name: /Replay\/Reprocess/i }))

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  // ADR-012 / ARS-004: the proposal that crosses the API boundary is keyed by
  // public route UUIDs — the active routes' own ids plus a proposal-only UUID
  // that has never been saved — and running a backtest must not touch the
  // active route configuration the draft was copied from.
  it('sends UUID route ids for the proposal and leaves the active configuration unchanged', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    renderWithProviders(<BacktestView />)
    // Wait for the seeded draft: once the diff reports an unchanged active
    // configuration, the routes are loaded and the draft can no longer be
    // re-seeded underneath an upload.
    await waitFor(() =>
      expect(screen.getByText(/Proposed configuration matches the current active configuration/i)).toBeInTheDocument()
    )

    const activeRoutes = await mock.listRoutes()
    const activeBefore = JSON.stringify(activeRoutes)
    const proposalOnlyUuid = '9f2d7c31-6b8e-4a15-9c4e-2d7f1b5a6e90'
    uploadRoutes([
      { ...activeRoutes[0] },
      { ...activeRoutes[1] },
      { ...activeRoutes[1], id: proposalOnlyUuid, name: 'Proposed Only Route', evaluation_order: 3 },
    ])
    await waitFor(() => expect(screen.getAllByText(/Loaded 3 proposed routes from file/i)).toHaveLength(2))

    fireEvent.click(screen.getByRole('button', { name: /Run backtest/i }))
    await waitFor(() => expect(screen.getByText(/Comparison results/i)).toBeInTheDocument())

    const call = fetchSpy.mock.calls.find(
      ([input, init]) => String(input) === '/api/backtest' && init?.method === 'POST'
    )
    expect(call).toBeDefined()
    const sent = JSON.parse(String(call![1]?.body)).proposed_configuration.routes as Route[]
    expect(sent.map((r) => r.id)).toEqual([activeRoutes[0].id, activeRoutes[1].id, proposalOnlyUuid])
    expect(sent.map((r) => r.name)).toEqual(['Music Discovery', 'Ambient', 'Proposed Only Route'])
    // Every route reference in the proposal is a UUID string, never a number.
    for (const route of sent) expect(route.id).toMatch(/^[0-9a-f-]{36}$/)

    expect(JSON.stringify(await mock.listRoutes())).toBe(activeBefore)
  })

  // The editor path is the default draft surface: saving into the draft must
  // rewrite the proposal without touching the active route list, and the
  // route's UUID must survive the edit unchanged.
  it('edits the draft without mutating the active configuration and keeps the route UUID', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch')
    renderWithProviders(<BacktestView />)
    await waitFor(() =>
      expect(screen.getByText(/Proposed configuration matches the current active configuration/i)).toBeInTheDocument()
    )

    const activeRoutes = await mock.listRoutes()
    const activeBefore = JSON.stringify(activeRoutes)

    const nameInputs = document.querySelectorAll<HTMLInputElement>('#route-name')
    expect(nameInputs).toHaveLength(activeRoutes.length)
    fireEvent.change(nameInputs[0], { target: { value: 'Draft Renamed Route' } })
    const saveButtons = screen.getAllByRole('button', { name: /^Save route$/ })
    expect(saveButtons).toHaveLength(activeRoutes.length)
    fireEvent.click(saveButtons[0])

    // The draft now differs from the active configuration in exactly one route,
    // still identified by that route's own UUID.
    await waitFor(() => expect(screen.getAllByText('modified')).toHaveLength(1))
    const header = screen.getAllByText('modified')[0].closest('div') as HTMLElement
    expect(header.textContent).toContain('Draft Renamed Route')
    expect(header.textContent).toContain(`(${activeRoutes[0].id.slice(0, 8)})`)
    expect(JSON.stringify(await mock.listRoutes())).toBe(activeBefore)

    fireEvent.click(screen.getByRole('button', { name: /Run backtest/i }))
    await waitFor(() => expect(screen.getByText(/Comparison results/i)).toBeInTheDocument())

    const call = fetchSpy.mock.calls.find(
      ([input, init]) => String(input) === '/api/backtest' && init?.method === 'POST'
    )
    expect(call).toBeDefined()
    const sent = JSON.parse(String(call![1]?.body)).proposed_configuration.routes as Route[]
    expect(sent.map((r) => r.id)).toEqual(activeRoutes.map((r) => r.id))
    expect(sent[0].name).toBe('Draft Renamed Route')
    expect(JSON.stringify(await mock.listRoutes())).toBe(activeBefore)
  })

  it('rejects an uploaded configuration whose route ids are not UUIDs', async () => {
    renderWithProviders(<BacktestView />)
    await waitFor(() =>
      expect(screen.getByText(/Proposed configuration matches the current active configuration/i)).toBeInTheDocument()
    )

    uploadRoutes([
      routeFixture({ id: 99 as unknown as string, name: 'Legacy Numeric Route' }),
    ])

    await waitFor(() =>
      expect(screen.getByText(/Legacy Numeric Route"\) has no valid route id/i)).toBeInTheDocument()
    )
    expect(screen.queryByText(/Loaded .* proposed routes from file/i)).not.toBeInTheDocument()
    // The unusable file never replaces the draft, so the diff still reflects
    // the untouched active configuration.
    expect(
      screen.getByText(/Proposed configuration matches the current active configuration/i)
    ).toBeInTheDocument()
  })

  it('keeps same-named routes as independent scenario-diff rows keyed by UUID', async () => {
    const uuidA = '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce'
    const uuidB = '4c7c3b7e-f09e-4bf4-9b55-aec0cf479b51'
    vi.spyOn(mock, 'listRoutes').mockResolvedValue([
      routeFixture({ id: uuidA, evaluation_order: 1 }),
      routeFixture({ id: uuidB, evaluation_order: 2 }),
    ])

    renderWithProviders(<BacktestView />)
    await waitFor(() =>
      expect(screen.getByText(/Proposed configuration matches the current active configuration/i)).toBeInTheDocument()
    )

    // Both routes share the display name "Music Discovery"; only their UUIDs
    // and the edited flag differ, so name-keyed grouping would collapse them.
    uploadRoutes([
      routeFixture({ id: uuidA, evaluation_order: 1, is_enabled: false }),
      routeFixture({ id: uuidB, evaluation_order: 2, is_enabled: false }),
    ])

    const badges = await screen.findAllByText('modified')
    expect(badges).toHaveLength(2)
    const headers = badges.map((badge) => badge.closest('div') as HTMLElement)
    const texts = headers.map((header) => header.textContent ?? '')
    // Two rows, both labelled "Music Discovery", each identified by its own
    // short UUID — the name alone never merges or swaps them.
    expect(texts).toHaveLength(2)
    for (const text of texts) expect(text).toContain('Music Discovery')
    expect(texts.some((text) => text.includes(`(${uuidA.slice(0, 8)})`))).toBe(true)
    expect(texts.some((text) => text.includes(`(${uuidB.slice(0, 8)})`))).toBe(true)
    expect(new Set(texts).size).toBe(2)
  })

  it('canonicalizes uppercase UUIDs so an uploaded route still matches its active identity', async () => {
    renderWithProviders(<BacktestView />)
    await waitFor(() =>
      expect(screen.getByText(/Proposed configuration matches the current active configuration/i)).toBeInTheDocument()
    )

    const active = await mock.listRoutes()
    // Same route, uppercase hex: without canonicalization the diff would split
    // it into an added row plus a removed row instead of one modified row.
    uploadRoutes([
      { ...active[0], id: active[0].id.toUpperCase(), is_enabled: false },
      { ...active[1] },
    ])

    await waitFor(() => expect(screen.getAllByText('modified')).toHaveLength(1))
    expect(screen.queryByText('added')).not.toBeInTheDocument()
    expect(screen.queryByText('removed')).not.toBeInTheDocument()
  })
})
