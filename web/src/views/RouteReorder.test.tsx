import { describe, it, expect, vi, beforeAll, afterAll, beforeEach } from 'vitest'
import type { MockInstance } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import { RoutesView } from './RoutesView'
import { ToastProvider } from '../components/ToastProvider'
import * as mock from '../api/mock'
import type { Route as RouteModel } from '../types'

const ROW_HEIGHT = 50
const HANDLE_X = 30

const ROUTE_A = '11111111-2222-4333-8444-555555555555'
const ROUTE_B = 'aaaabbbb-cccc-4ddd-8eee-ffff00000000'
const ROUTE_C = 'bbbbcccc-dddd-4eee-8fff-000011112222'

const routeFixture = (overrides: Partial<RouteModel> = {}): RouteModel => ({
  id: ROUTE_A,
  name: 'Music Discovery',
  description: '',
  evaluation_order: 1,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [],
  actions: [],
  exclusions: [],
  match_count: 0,
  created_at: '2026-08-01T10:00:00Z',
  updated_at: '2026-09-08T10:00:00Z',
  ...overrides,
})

const threeRoutes = () => [
  routeFixture(),
  routeFixture({ id: ROUTE_B, name: 'Ambient', evaluation_order: 2 }),
  routeFixture({ id: ROUTE_C, name: 'Focus', evaluation_order: 3 }),
]

function domRect(x: number, y: number, width: number, height: number): DOMRect {
  return {
    x,
    y,
    width,
    height,
    left: x,
    top: y,
    right: x + width,
    bottom: y + height,
    toJSON: () => ({}),
  }
}

/**
 * Rows the app rendered, in visual order. During a drag dnd-kit inserts a
 * hidden placeholder copy of the dragged row, which keeps its slot in the
 * layout but is not a route row of its own.
 */
function dataRows(): HTMLTableRowElement[] {
  return Array.from(
    document.querySelectorAll<HTMLTableRowElement>('tbody > [data-route-row]:not([data-dnd-placeholder])')
  )
}

function layoutRows(): HTMLTableRowElement[] {
  return Array.from(document.querySelectorAll<HTMLTableRowElement>('tbody > [data-route-row]'))
}

// jsdom has no layout engine: give route rows stacked rects so dnd-kit's
// collision detection, hit testing, and visibility checks resolve, and give
// ancestors a viewport-sized rect so rows are not clipped away.
function layoutRect(el: Element): DOMRect {
  const rows = layoutRows()
  const rowIndex = rows.indexOf(el as HTMLTableRowElement)
  if (rowIndex !== -1) return domRect(0, rowIndex * ROW_HEIGHT, 600, ROW_HEIGHT)
  const ownerRow = el.closest('[data-route-row]')
  if (ownerRow) {
    const index = rows.indexOf(ownerRow as HTMLTableRowElement)
    return domRect(8, index * ROW_HEIGHT + 8, 44, 44)
  }
  return domRect(0, 0, 1024, 768)
}

function rowAtPoint(_x: number, y: number): HTMLTableRowElement | null {
  const rows = layoutRows()
  if (rows.length === 0) return null
  const index = Math.max(0, Math.min(Math.floor(y / ROW_HEIGHT), rows.length - 1))
  return rows[index]
}

/** jsdom has no layout observers; dnd-kit only needs them to be constructible. */
class ObserverStub {
  root = null
  rootMargin = ''
  thresholds: number[] = []
  observe() {}
  unobserve() {}
  disconnect() {}
  takeRecords(): never[] {
    return []
  }
}

let rectSpy: MockInstance
const originalPointerCapture = Element.prototype.setPointerCapture
const originalReleasePointerCapture = Element.prototype.releasePointerCapture
const originalHasPointerCapture = Element.prototype.hasPointerCapture
const originalGetAnimations = Element.prototype.getAnimations
const originalAnimate = Element.prototype.animate

beforeAll(() => {
  rectSpy = vi
    .spyOn(Element.prototype, 'getBoundingClientRect')
    .mockImplementation(function (this: Element) {
      return layoutRect(this)
    })
  // jsdom implements neither pointer capture nor the Web Animations API, both
  // of which dnd-kit's sensors and drop animation use, and it has no layout
  // observers or hit testing for its collision detection.
  Element.prototype.setPointerCapture = () => {}
  Element.prototype.releasePointerCapture = () => {}
  Element.prototype.hasPointerCapture = () => false
  Element.prototype.getAnimations = () => []
  Element.prototype.animate = (() => ({ finished: Promise.resolve(null) })) as unknown as typeof Element.prototype.animate
  document.getAnimations = () => []
  document.elementFromPoint = rowAtPoint
  // dnd-kit checks prefers-reduced-motion through matchMedia.
  window.matchMedia = ((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia
  vi.stubGlobal('IntersectionObserver', ObserverStub)
  vi.stubGlobal('ResizeObserver', ObserverStub)
})

afterAll(() => {
  rectSpy.mockRestore()
  vi.unstubAllGlobals()
  delete document.elementFromPoint
  delete document.getAnimations
  Element.prototype.setPointerCapture = originalPointerCapture
  Element.prototype.releasePointerCapture = originalReleasePointerCapture
  Element.prototype.hasPointerCapture = originalHasPointerCapture
  Element.prototype.getAnimations = originalGetAnimations
  Element.prototype.animate = originalAnimate
})

function renderView() {
  return render(
    <ToastProvider>
      <MemoryRouter initialEntries={['/routes']}>
        <Routes>
          <Route path="/routes" element={<RoutesView />} />
        </Routes>
      </MemoryRouter>
    </ToastProvider>
  )
}

async function renderThreeRoutes() {
  vi.spyOn(mock, 'listRoutes').mockResolvedValueOnce(threeRoutes())
  renderView()
  await waitFor(() => expect(screen.getByText('Music Discovery')).toBeInTheDocument())
}

function gripOf(routeName: string): HTMLElement {
  return screen.getByRole('button', { name: `Reorder ${routeName}` })
}

function press(handle: HTMLElement, key: string) {
  fireEvent.keyDown(handle, { key })
}

/** Press ArrowDown once per step and wait for the drag to register each move. */
async function moveByKeyboard(routeName: string, steps = 1) {
  for (let i = 0; i < steps; i += 1) {
    press(gripOf(routeName), 'ArrowDown')
    await waitFor(
      () => {
        const dragging = document.querySelector('[data-dnd-dragging]') as HTMLElement | null
        expect(dragging?.style.getPropertyValue('--dnd-translate') ?? '').not.toBe('0px 0px 0')
      },
      { timeout: 3000 }
    )
  }
}

function pointerEventInit(y: number) {
  return { pointerId: 1, isPrimary: true, button: 0, pointerType: 'mouse', clientX: HANDLE_X, clientY: y }
}

describe('RoutesView reordering', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('reorders by keyboard drag from the grip handle and persists through the reorder API', async () => {
    const reorder = vi.spyOn(mock, 'reorderRoutes').mockResolvedValue(undefined)
    await renderThreeRoutes()

    const handle = gripOf('Music Discovery (11111111)')
    handle.focus()
    press(handle, ' ')
    await waitFor(() => expect(document.querySelector('[data-dnd-dragging]')).not.toBeNull())
    await moveByKeyboard('Music Discovery (11111111)')
    press(gripOf('Music Discovery (11111111)'), ' ')

    await waitFor(() => expect(reorder).toHaveBeenCalledWith([ROUTE_B, ROUTE_A, ROUTE_C]))
    await waitFor(() => expect(dataRows()[1]).toHaveTextContent('Music Discovery'))
    expect(dataRows()[0]).toHaveTextContent('Ambient')
    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent('Route order updated'))
  })

  it('cancels a drag on Escape without persisting or changing the order', async () => {
    const reorder = vi.spyOn(mock, 'reorderRoutes').mockResolvedValue(undefined)
    await renderThreeRoutes()

    const handle = gripOf('Music Discovery (11111111)')
    handle.focus()
    press(handle, ' ')
    await waitFor(() => expect(document.querySelector('[data-dnd-dragging]')).not.toBeNull())
    press(handle, 'ArrowDown')
    press(handle, 'Escape')

    await waitFor(() => expect(document.querySelector('[data-dnd-dragging]')).toBeNull())
    expect(reorder).not.toHaveBeenCalled()
    expect(document.querySelector('.toast')).toBeNull()
    expect(dataRows()[0]).toHaveTextContent('Music Discovery')
    expect(dataRows()[1]).toHaveTextContent('Ambient')
  })

  it('starts a pointer drag only from the grip handle and cancels it with Escape', async () => {
    const reorder = vi.spyOn(mock, 'reorderRoutes').mockResolvedValue(undefined)
    await renderThreeRoutes()

    const row = dataRows()[0]
    for (const trigger of [
      row.querySelector(`a[href="/routes/${ROUTE_A}"]`) as HTMLElement,
      row.querySelector('[role="switch"]') as HTMLElement,
      row.querySelector('[aria-label="Move Music Discovery (11111111) down"]') as HTMLElement,
    ]) {
      fireEvent.pointerDown(trigger, pointerEventInit(30))
      fireEvent.pointerMove(document, pointerEventInit(125))
      expect(document.querySelector('[data-dnd-dragging]')).toBeNull()
      expect(document.querySelector('[data-dnd-placeholder]')).toBeNull()
      fireEvent.pointerUp(document, pointerEventInit(125))
    }
    expect(reorder).not.toHaveBeenCalled()

    // The same pointer sequence on the grip does start a drag.
    fireEvent.pointerDown(gripOf('Music Discovery (11111111)'), pointerEventInit(30))
    await waitFor(() => expect(document.querySelector('[data-dnd-dragging]')).not.toBeNull())
    fireEvent.pointerMove(document, pointerEventInit(125))
    fireEvent.keyDown(gripOf('Music Discovery (11111111)'), { key: 'Escape' })

    await waitFor(() => expect(document.querySelector('[data-dnd-dragging]')).toBeNull())
    expect(reorder).not.toHaveBeenCalled()
    expect(dataRows()[0]).toHaveTextContent('Music Discovery')
  })

  it('reverts the order and shows an error toast when a dragged reorder fails to persist', async () => {
    vi.spyOn(mock, 'reorderRoutes').mockRejectedValue(new Error('reorder failed'))
    await renderThreeRoutes()

    const handle = gripOf('Music Discovery (11111111)')
    handle.focus()
    press(handle, ' ')
    await waitFor(() => expect(document.querySelector('[data-dnd-dragging]')).not.toBeNull())
    await moveByKeyboard('Music Discovery (11111111)')
    press(gripOf('Music Discovery (11111111)'), ' ')

    await waitFor(() => expect(document.querySelector('.toast')).toHaveTextContent(/reorder failed/i))
    await waitFor(() => expect(dataRows()[0]).toHaveTextContent('Music Discovery'))
    expect(dataRows()[1]).toHaveTextContent('Ambient')
    expect(dataRows()[2]).toHaveTextContent('Focus')
  })

  it('keeps Move Up/Down as focusable keyboard alternatives on the same reorder path', async () => {
    const reorder = vi.spyOn(mock, 'reorderRoutes').mockResolvedValue(undefined)
    await renderThreeRoutes()

    const moveDown = screen.getByRole('button', { name: 'Move Music Discovery (11111111) down' })
    moveDown.focus()
    expect(moveDown).toHaveFocus()

    // Browsers activate a focused button with Enter or Space; the click runs
    // the same move-and-persist path a drag drop uses.
    fireEvent.click(moveDown)

    await waitFor(() => expect(reorder).toHaveBeenCalledWith([ROUTE_B, ROUTE_A, ROUTE_C]))
    expect(screen.getByRole('button', { name: 'Move Ambient (aaaabbbb) up' })).toBeInTheDocument()
  })
})
