import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { Sidebar } from './Sidebar'
import { BottomNav } from './BottomNav'
import App from '../App'

describe('Videos navigation', () => {
  it('lists Videos directly after Runs in the sidebar', () => {
    render(
      <MemoryRouter>
        <Sidebar />
      </MemoryRouter>
    )
    const labels = within(screen.getByRole('navigation', { name: 'Main navigation' }))
      .getAllByRole('link')
      .map((a) => a.textContent)
    expect(labels.indexOf('Videos')).toBe(labels.indexOf('Runs') + 1)
    expect(screen.getByRole('link', { name: 'Videos' })).toHaveAttribute('href', '/videos')
  })

  it('keeps the bottom bar at five items and puts Videos under More', () => {
    render(
      <MemoryRouter>
        <BottomNav />
      </MemoryRouter>
    )
    const bar = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(within(bar).getAllByRole('listitem')).toHaveLength(5)
    expect(within(bar).queryByRole('link', { name: 'Videos' })).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'More' }))
    expect(within(screen.getByRole('menu')).getByRole('menuitem', { name: 'Videos' })).toHaveAttribute('href', '/videos')
  })

  it('routes /videos and /videos/:videoId', async () => {
    const { unmount } = render(
      <MemoryRouter initialEntries={['/videos']}>
        <App />
      </MemoryRouter>
    )
    expect(screen.getByRole('heading', { level: 1, name: 'Videos' })).toBeInTheDocument()
    unmount()
    render(
      <MemoryRouter initialEntries={['/videos/bad']}>
        <App />
      </MemoryRouter>
    )
    expect(screen.getByText(/is not a valid video ID/)).toBeInTheDocument()
  })
})
