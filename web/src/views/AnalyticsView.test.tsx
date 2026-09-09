import { describe, it, expect } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { AnalyticsView } from './AnalyticsView'

describe('AnalyticsView', () => {
  it('renders trend charts and a data table toggle', async () => {
    render(<AnalyticsView />)
    await waitFor(() => expect(screen.getByText('Acceptance rate')).toBeInTheDocument())

    expect(screen.getByText('Volume & outcome breakdown')).toBeInTheDocument()
    expect(screen.getByText('Route match rates')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /Show data table/i }))
    await waitFor(() => expect(screen.getByRole('table')).toBeInTheDocument())
  })
})
