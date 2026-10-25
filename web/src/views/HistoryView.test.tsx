import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { HistoryView } from './HistoryView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { ConfigDiffResult, ConfigVersion } from '../types'

function renderWithProviders(ui: React.ReactElement, initialEntries = ['/history']) {
  return render(
    <MemoryRouter initialEntries={initialEntries}>
      <ToastProvider>
        <Routes>
          <Route path="/history" element={ui} />
          <Route path="/history/:versionId" element={ui} />
        </Routes>
      </ToastProvider>
    </MemoryRouter>
  )
}

describe('HistoryView', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  const recordedExplanation = 'This is a recorded configuration value, not a current handling choice. Current evaluation continues after duplicate matches regardless of this value, including replay and backtest. It does not establish what actions occurred in earlier runs; inspect their recorded outcomes and actions.'

  it.each(['skip', 'flag', 'continue', '  <legacy> & custom  ', 'x'.repeat(300), '', undefined])(
    'preserves the recorded summary value %j and explains it in reading order', async (value) => {
      const snapshot = { routes: [], duplicate_threshold: 0.65, ...(value === undefined ? {} : { duplicate_behavior: value }) }
      // Omitted fields in old HTTP snapshots must be displayable as well.
      const version = { id: 7, created_at: '2026-08-02T10:00:00Z', change_description: 'Previously skip', snapshot } as ConfigVersion
      vi.spyOn(mock, 'listConfigVersions').mockResolvedValueOnce([version])
      vi.spyOn(mock, 'diffConfigVersion').mockResolvedValueOnce({ from_id: 6, to_id: 7, settings_changed: {}, routes_added: [], routes_removed: [], routes_changed: [] })
      renderWithProviders(<HistoryView />, ['/history/7'])

      const row = await screen.findByText(/^Duplicate handling \(recorded legacy value\):/)
      expect(row.querySelector('span')?.textContent).toBe(value === undefined || value === '' ? 'Empty or not recorded' : value)
      expect(row.previousElementSibling).toHaveTextContent('Duplicate threshold: 0.65')
      expect(row.nextElementSibling?.textContent).toBe(recordedExplanation)
      expect(row.querySelector('span')).toHaveClass('recorded-duplicate-value')
      expect(row.querySelector('legacy')).toBeNull()
      expect(row.querySelector('input, button, select, [tabindex]')).toBeNull()
      expect(screen.getByText('Previously skip')).toBeInTheDocument()
      expect(await screen.findByText('No changes between these versions.')).toBeInTheDocument()
      expect(screen.queryByText('Duplicate handling (recorded legacy values)')).not.toBeInTheDocument()
      expect(snapshot.duplicate_behavior).toBe(value)
    },
  )

  it.each([['skip', 'continue'], ['flag', 'skip'], ['', 'continue'], [undefined, 'continue'], ['continue', ''], ['continue', undefined]])(
    'labels recorded diff values %j → %j without implying a policy or action change', async (old, next) => {
      const diff = { from_id: 1, to_id: 1, settings_changed: { duplicate_behavior: { old, new: next }, duplicate_threshold: { old: '0.5', new: '0.6' } }, routes_added: [], routes_removed: [], routes_changed: [] } as ConfigDiffResult
      vi.spyOn(mock, 'diffConfigVersion').mockResolvedValueOnce(diff)
      renderWithProviders(<HistoryView />, ['/history/1'])
      const label = await screen.findByText('Duplicate handling (recorded legacy values)')
      const row = label.closest('li')!
      expect(label.nextElementSibling?.textContent).toBe(`Previous: ${old === undefined || old === '' ? 'Empty or not recorded' : old}`)
      expect(label.nextElementSibling?.nextElementSibling?.textContent).toBe(`This version: ${next === undefined || next === '' ? 'Empty or not recorded' : next}`)
      expect(row.lastElementChild?.textContent).toBe(recordedExplanation)
      expect(row.querySelector('input, button, select, [tabindex]')).toBeNull()
      expect(screen.getByText(/^duplicate_threshold:/).textContent).toBe('duplicate_threshold: 0.5 → 0.6')
      expect(diff.settings_changed.duplicate_behavior).toEqual({ old, new: next })
    },
  )

  it('lists configuration versions with timestamps and snapshot summary', async () => {
    renderWithProviders(<HistoryView />)

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration History/i })).toBeInTheDocument())

    const versionRow = screen.getByText('Initial configuration').closest('tr')
    expect(versionRow).not.toBeNull()
    expect(within(versionRow!).getByText('2')).toBeInTheDocument() // route count
  })

  it('navigates to version detail and shows restore button', async () => {
    renderWithProviders(<HistoryView />)

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration History/i })).toBeInTheDocument())

    fireEvent.click(screen.getByRole('link', { name: /View/i }))

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration Version 1/i })).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /Restore this version/i })).toBeInTheDocument()
    expect(screen.getByText(/Route snapshot/i)).toBeInTheDocument()
  })

  it('renders route snapshot actions with the canonical public action type', async () => {
    renderWithProviders(<HistoryView />, ['/history/1'])

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration Version 1/i })).toBeInTheDocument())
    // Configuration-history action representations use the public API value (ADR-007/ARS-001).
    expect(await screen.findAllByText('add_to_playlist → PLmusic')).not.toHaveLength(0)
  })

  it('restores a configuration version from the list', async () => {
    const restoreSpy = vi.spyOn(mock, 'restoreConfigVersion')
    renderWithProviders(<HistoryView />)

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration History/i })).toBeInTheDocument())

    fireEvent.click(screen.getByRole('button', { name: /Restore/i }))

    await waitFor(() => expect(restoreSpy).toHaveBeenCalledWith(1, 'Restored from version 1'))
    expect(await screen.findAllByText(/Configuration restored/i)).toHaveLength(2)
  })

  it('displays a diff on the version detail page', async () => {
    const diffSpy = vi.spyOn(mock, 'diffConfigVersion')
    renderWithProviders(<HistoryView />, ['/history/1'])

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration Version 1/i })).toBeInTheDocument())
    await waitFor(() => expect(diffSpy).toHaveBeenCalledWith(1, undefined))

    expect(await screen.findByText(/Comparing version 1 → version 1/i, undefined, { timeout: 3000 })).toBeInTheDocument()
  })

  it('renders diff without crashing when server returns null arrays', async () => {
    const nullDiff = {
      from_id: 1,
      to_id: 1,
      settings_changed: {},
      routes_added: null,
      routes_removed: null,
      routes_changed: null,
    } as unknown as ConfigDiffResult
    vi.spyOn(mock, 'diffConfigVersion').mockResolvedValueOnce(nullDiff)
    renderWithProviders(<HistoryView />, ['/history/1'])

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration Version 1/i })).toBeInTheDocument())
    expect(await screen.findByText(/No changes between these versions/i)).toBeInTheDocument()
  })

  // Two routes may share a display name (BR-003 AC10); snapshot and diff
  // entries must stay distinguishable by short UUID suffix, keyed by UUID.
  it('disambiguates same-named routes in the snapshot and diff with short UUID suffixes', async () => {
    const routeIdA = '3a554e96-47ba-4f3d-a7bb-a61e3f50c1ce'
    const routeIdB = '4c7c3b7e-f09e-4bf4-9b55-aec0cf479b51'
    const routeIdC = 'b7e9d4f2-1c2a-4d3b-8e5f-9a0b1c2d3e4f'
    const baseRoute = {
      description: '',
      evaluation_order: 1,
      is_final: false,
      is_enabled: true,
      condition_composition: 'AND' as const,
      conditions: [],
      actions: [],
      exclusions: [],
      created_at: '2026-08-01T10:00:00Z',
      updated_at: '2026-08-01T10:00:00Z',
    }
    const version: ConfigVersion = {
      id: 7,
      created_at: '2026-08-02T10:00:00Z',
      change_description: 'Duplicate names',
      snapshot: {
        routes: [
          { ...baseRoute, id: routeIdA, name: 'Ambient' },
          { ...baseRoute, id: routeIdB, name: 'Ambient' },
        ],
        duplicate_threshold: 0.5,
        duplicate_behavior: 'skip',
      },
    }
    const diff: ConfigDiffResult = {
      from_id: 6,
      to_id: 7,
      settings_changed: {},
      routes_added: [{ ...baseRoute, id: routeIdA, name: 'Ambient' }],
      routes_removed: [{ ...baseRoute, id: routeIdB, name: 'Ambient' }],
      routes_changed: [
        {
          id: routeIdC,
          old: { ...baseRoute, id: routeIdC, name: 'Ambient' },
          new: { ...baseRoute, id: routeIdC, name: 'Ambient', is_enabled: false },
          changes: [{ field: 'is_enabled', old: 'true', new: 'false' }],
        },
      ],
    }
    vi.spyOn(mock, 'listConfigVersions').mockResolvedValue([version])
    vi.spyOn(mock, 'diffConfigVersion').mockResolvedValue(diff)

    renderWithProviders(<HistoryView />, ['/history/7'])

    await waitFor(() => expect(screen.getByRole('heading', { name: /Configuration Version 7/i })).toBeInTheDocument())

    // Route snapshot: both same-named routes carry their distinct short UUID.
    // Each suffix also appears once more inside the diff sections below, so
    // assert presence via the inclusive finder.
    expect((await screen.findAllByText('(3a554e96)')).length).toBeGreaterThanOrEqual(1)
    expect((await screen.findAllByText('(4c7c3b7e)')).length).toBeGreaterThanOrEqual(1)

    // Diff: added, removed, and changed entries are identified by short UUID,
    // not by name, so the same-named pair stays unambiguous.
    await screen.findByText(/Routes added/i)
    const added = screen.getByText(/Routes added/i).parentElement!
    const removed = screen.getByText(/Routes removed/i).parentElement!
    const changed = screen.getByText(/Routes changed/i).parentElement!
    expect(added.textContent).toContain('(3a554e96)')
    expect(removed.textContent).toContain('(4c7c3b7e)')
    expect(changed.textContent).toContain('(b7e9d4f2)')
    expect(changed.textContent).toContain('is_enabled')
  })

  it('shows an accessible fetch-error state instead of the empty state when history fetch fails', async () => {
    vi.spyOn(mock, 'listConfigVersions').mockRejectedValueOnce(new Error('network down'))

    renderWithProviders(<HistoryView />)

    const alert = await waitFor(() => screen.getByRole('alert'))
    expect(alert).toHaveTextContent(
      'Failed to load configuration history. Check your connection and try again.'
    )
    expect(alert).toHaveAttribute('aria-live', 'assertive')

    const retry = screen.getByRole('button', { name: /^Retry$/ })
    await waitFor(() => expect(retry).toHaveFocus())

    // A failed fetch never renders the empty state.
    expect(screen.queryByText(/No configuration changes yet/)).not.toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
  })

  it('renders the empty state only when the fetch succeeds with an empty list', async () => {
    vi.spyOn(mock, 'listConfigVersions').mockResolvedValueOnce([])

    renderWithProviders(<HistoryView />)

    await waitFor(() => expect(screen.getByText('No configuration changes yet.')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('recovers to the version list after a successful retry', async () => {
    vi.spyOn(mock, 'listConfigVersions').mockRejectedValueOnce(new Error('network down'))

    renderWithProviders(<HistoryView />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(screen.getByText('Initial configuration')).toBeInTheDocument())
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('stays in the fetch-error state and re-enables retry when retry also fails', async () => {
    vi.spyOn(mock, 'listConfigVersions')
      .mockRejectedValueOnce(new Error('network down'))
      .mockRejectedValueOnce(new Error('still down'))

    renderWithProviders(<HistoryView />)

    const retry = await waitFor(() => screen.getByRole('button', { name: /^Retry$/ }))
    fireEvent.click(retry)

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/Retry failed. Please try again./i))
    expect(
      screen.getByText('Failed to load configuration history. Check your connection and try again.')
    ).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('button', { name: /^Retry$/ })).toBeEnabled())
    expect(screen.queryByText(/No configuration changes yet/)).not.toBeInTheDocument()
  })
})
