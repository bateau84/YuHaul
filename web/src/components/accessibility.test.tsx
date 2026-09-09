import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { BrowserRouter } from 'react-router-dom'
import { Sidebar } from '../components/Sidebar'
import { RouteEditor } from '../components/RouteEditor'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { ToastProvider, useToast } from '../components/ToastProvider'
import type { Route } from '../types'

const testRoute: Route = {
  id: 1,
  name: 'Music Discovery',
  description: '',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [],
  actions: [],
  exclusions: [],
  created_at: '',
  updated_at: '',
}

function ToastTrigger({ message, type }: { message: string; type: 'success' | 'error' }) {
  const { addToast } = useToast()
  return (
    <button type="button" onClick={() => addToast(message, type)}>
      Trigger
    </button>
  )
}

describe('accessibility', () => {
  it('Sidebar is a nav landmark with an accessible label', () => {
    render(
      <BrowserRouter>
        <Sidebar />
      </BrowserRouter>
    )
    const nav = screen.getByRole('navigation', { name: /Main navigation/i })
    expect(nav).toBeInTheDocument()
    expect(nav).toHaveAttribute('aria-label', 'Main navigation')
  })

  it('RouteEditor enabled toggle exposes role=switch and aria-checked', () => {
    render(<RouteEditor route={testRoute} onSave={() => {}} onCancel={() => {}} />)
    const toggle = screen.getByRole('switch')
    expect(toggle).toBeInTheDocument()
    expect(toggle).toHaveAttribute('aria-checked', 'true')
    expect(toggle).toHaveAttribute('aria-label', 'Disable route Music Discovery')
  })

  it('ConfirmDialog labels itself via aria-labelledby', () => {
    HTMLDialogElement.prototype.showModal = vi.fn()
    HTMLDialogElement.prototype.close = vi.fn()
    render(
      <ConfirmDialog open title="Delete route?" onConfirm={() => {}} onCancel={() => {}}>
        This action cannot be undone.
      </ConfirmDialog>
    )
    const dialog = document.querySelector('dialog')
    expect(dialog).toBeInTheDocument()
    const labelledBy = dialog?.getAttribute('aria-labelledby')
    expect(labelledBy).toBeTruthy()
    const title = document.getElementById(labelledBy!)
    expect(title).toHaveTextContent('Delete route?')
  })

  it('error toasts use role=alert and assertive aria-live', async () => {
    render(
      <ToastProvider>
        <ToastTrigger message="Something failed" type="error" />
      </ToastProvider>
    )
    fireEvent.click(screen.getByRole('button', { name: /Trigger/i }))
    const toast = await waitFor(() => screen.getByRole('alert'))
    expect(toast).toHaveTextContent('Something failed')
    expect(toast).toHaveAttribute('aria-live', 'assertive')
  })

  it('success toasts keep role=status and polite aria-live', async () => {
    render(
      <ToastProvider>
        <ToastTrigger message="It worked" type="success" />
      </ToastProvider>
    )
    fireEvent.click(screen.getByRole('button', { name: /Trigger/i }))
    const toast = await waitFor(() => document.querySelector('.toast-region .toast'))
    expect(toast).toHaveTextContent('It worked')
    expect(toast).toHaveAttribute('role', 'status')
    expect(toast).toHaveAttribute('aria-live', 'polite')
  })
})
