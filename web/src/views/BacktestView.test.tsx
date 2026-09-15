import { describe, it, expect } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { BacktestView } from './BacktestView'
import { ToastProvider } from '../components/ToastProvider'

function renderWithProviders(ui: React.ReactElement) {
  return render(
    <MemoryRouter>
      <ToastProvider>{ui}</ToastProvider>
    </MemoryRouter>
  )
}

describe('BacktestView', () => {
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
      [JSON.stringify({ routes: [{ name: 'Uploaded Route', id: 99, evaluation_order: 1, is_final: false, is_enabled: true, condition_composition: 'AND', conditions: [], actions: [], exclusions: [], created_at: '', updated_at: '' }] })],
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
})
