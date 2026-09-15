import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { HistoryView } from './HistoryView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'

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
})
