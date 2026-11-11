import { describe, expect, it } from 'vitest'
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import type { CausalExclusion, RouteDecisionSummary } from '../types'
import { RouteResults } from './RouteResults'

const uuid = (n: number) => `${String(n).padStart(8, '0')}-aaaa-4bbb-8ccc-dddddddddddd`

const cause: CausalExclusion = {
  status: 'available', config_version_id: 42, exclusion_index: 2, semantics_version: 1,
  exclusion_type: 'duration_min', field: 'duration_seconds', comparison: 'strict_less_than',
  configured_value: '75', configured_value_status: 'available', matched: true, compared_input_status: 'unavailable',
}

const route = (n: number, over: Partial<RouteDecisionSummary> = {}): RouteDecisionSummary => ({
  id: uuid(n), route_id: uuid(n), route_name: `Route ${n}`, route_name_source: 'run_snapshot',
  evaluation_order: n, decision: 'matched', ...over,
})

// What a sighted reader sees: the screen-reader-only spoken twins removed.
function visibleText(el: HTMLElement): string {
  const copy = el.cloneNode(true) as HTMLElement
  copy.querySelectorAll('.sr-only').forEach((n) => n.remove())
  return copy.textContent ?? ''
}

function renderLines(decisions: RouteDecisionSummary[], simulation = false) {
  return render(
    <MemoryRouter>
      <RouteResults decisions={decisions} runId={7} videoId="vid1" videoTitle="My Video" simulation={simulation} />
    </MemoryRouter>,
  )
}

describe('RouteResults', () => {
  it('shows one line per route with label, outcome and reason, in recorded order', () => {
    renderLines([
      route(1, { route_name: 'New Listens', decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: cause }),
      route(2, { route_name: 'Fireship', decision: 'not_matched', failed_condition: { field: 'channel_name', operator: 'equals', value: 'Fireship', composition: 'AND', condition_count: 1, further_failed: 0 } }),
      route(3, { route_name: 'Music Discovery' }),
    ])
    const items = within(screen.getByRole('list', { name: 'Route results' })).getAllByRole('listitem')
    expect(items).toHaveLength(3)
    expect(items.map(visibleText)).toEqual([
      'New Listens (00000001) — excluded: duration < 75 sDetails',
      'Fireship (00000002) — no match: needs channel name = “Fireship”Details',
      'Music Discovery (00000003) — matchedDetails',
    ])
  })

  it('reads “— no match” alone when the list read has no failed condition', () => {
    renderLines([route(1, { decision: 'not_matched' })])
    const item = screen.getByRole('listitem')
    expect(item).toHaveTextContent('Route 1 (00000001) — no matchDetails')
    expect(item.textContent).not.toContain(':')
  })

  it('hides symbols from assistive technology and supplies the spoken wording', () => {
    renderLines([route(1, { decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: cause })])
    const symbol = screen.getByText('duration < 75 s')
    expect(symbol).toHaveAttribute('aria-hidden', 'true')
    const spoken = screen.getByText('duration less than 75 seconds')
    expect(spoken).toHaveClass('sr-only')
    expect(spoken).not.toHaveAttribute('aria-hidden')
  })

  it('labels the group for simulations and prefixes the details', () => {
    renderLines([route(1)], true)
    expect(screen.getByRole('list', { name: 'Simulated route results' })).toBeInTheDocument()
    expect(screen.queryByRole('list', { name: 'Route results' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Details for route Route 1 (00000001)' }))
    const region = document.getElementById(screen.getByRole('button', { name: /Details for route/ }).getAttribute('aria-controls')!)!
    expect(region.firstElementChild).toHaveTextContent('Simulated result. No playlist was changed.')
  })

  it('does not label production lines as simulated', () => {
    renderLines([route(1)])
    fireEvent.click(screen.getByRole('button', { name: /Details for route/ }))
    expect(screen.queryByText('Simulated result. No playlist was changed.')).not.toBeInTheDocument()
  })

  describe('Details disclosure', () => {
    it('is closed by default, toggles aria-expanded, and keeps focus where it was', () => {
      renderLines([route(1, { decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: cause })])
      const button = screen.getByRole('button', { name: 'Details for route Route 1 (00000001)' })
      expect(button).toHaveTextContent('Details')
      expect(button).toHaveAttribute('aria-expanded', 'false')
      const region = document.getElementById(button.getAttribute('aria-controls')!)!
      expect(region).not.toBeVisible()
      expect(screen.queryByText(/Recorded causal exclusion/)).not.toBeInTheDocument()

      button.focus()
      fireEvent.click(button)
      expect(button).toHaveAttribute('aria-expanded', 'true')
      expect(region).toBeVisible()
      expect(document.activeElement).toBe(button)
      expect(screen.queryByRole('status')).not.toBeInTheDocument()

      fireEvent.click(button)
      expect(button).toHaveAttribute('aria-expanded', 'false')
      expect(region).not.toBeVisible()
    })

    it('shows the full record of a captured exclusion in the accepted order', () => {
      renderLines([route(1, { decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: cause })], true)
      fireEvent.click(screen.getByRole('button', { name: /Details for route/ }))
      const paragraphs = Array.from(document.querySelectorAll('[id] p')).map((p) => p.textContent)
      expect(paragraphs).toEqual([
        'Simulated result. No playlist was changed.',
        `Route: Route 1 (ID: ${uuid(1)})`,
        'Recorded causal exclusion: configuration 42, exclusion #3.',
        'Matched rule: Duration is strictly less than configured minimum 75 seconds.',
        'Conditions were not evaluated for this route because this exclusion matched.',
        'Compared input unavailable in this rule-reference record.',
        'Open full decision detail',
      ])
      expect(screen.getByRole('link', { name: 'Open full decision detail' })).toHaveAttribute('href', '/runs/7/video/vid1')
    })

    it('explains a current/local name and an unavailable name', () => {
      renderLines([
        route(1, { route_name_source: 'current_local' }),
        route(2, { route_name_source: 'unavailable', route_name: '' }),
      ])
      fireEvent.click(screen.getByRole('button', { name: 'Details for route Route 1 (00000001)' }))
      expect(screen.getByText(`Route: Route 1 (current/local name; ID: ${uuid(1)})`)).toBeInTheDocument()
      expect(screen.getByText('This is the current route name, not necessarily the name when this run was evaluated.')).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', { name: 'Details for route name unavailable (00000002)' }))
      expect(screen.getByText(`Route name unavailable (ID: ${uuid(2)})`)).toBeInTheDocument()
    })

    it('shows incomplete states with their full sentences, not the compact wording', () => {
      renderLines([route(1, { decision: 'skipped', skip_reason: 'exclusion', causal_exclusion: { ...cause, status: 'definition_unavailable', matched: null } })])
      expect(screen.getByRole('listitem')).toHaveTextContent('excluded: rule definition unavailable (record incomplete)')
      fireEvent.click(screen.getByRole('button', { name: /Details for route/ }))
      expect(screen.getByText('Route exclusion recorded; the captured rule definition is unavailable. This explanation is incomplete.')).toBeInTheDocument()
      expect(screen.getByText('Captured reference: configuration 42, exclusion #3.')).toBeInTheDocument()
      expect(screen.getByText('Compared input unavailable in this rule-reference record.')).toBeInTheDocument()
    })

    it('shows the failed condition in full, as recorded, and points to detail for the rest', () => {
      const long = 'v'.repeat(60)
      renderLines([route(1, { decision: 'not_matched', failed_condition: { field: 'title', operator: 'contains', value: long, composition: 'AND', condition_count: 3, further_failed: 2 } })])
      expect(screen.getByRole('listitem')).toHaveTextContent(`needs title contains “${'v'.repeat(40)}…” (+2 more)`)
      fireEvent.click(screen.getByRole('button', { name: /Details for route/ }))
      expect(screen.getByText(`title contains “${long}” — not met`)).toBeInTheDocument()
      expect(screen.getByText('2 more conditions were not met. Condition details are in the full decision detail.')).toBeInTheDocument()
    })

    it('points to the full detail when the list read carries no failed condition', () => {
      renderLines([route(1, { decision: 'not_matched' })])
      fireEvent.click(screen.getByRole('button', { name: /Details for route/ }))
      expect(screen.getByText('Condition details are in the full decision detail.')).toBeInTheDocument()
    })

    it('opens one line without changing the others', () => {
      renderLines([route(1), route(2)])
      const [first, second] = screen.getAllByRole('button', { name: /Details for route/ })
      fireEvent.click(first)
      expect(first).toHaveAttribute('aria-expanded', 'true')
      expect(second).toHaveAttribute('aria-expanded', 'false')
    })

    it('does not leave a dangling aria-controls', () => {
      renderLines([route(1)])
      const id = screen.getByRole('button', { name: /Details for route/ }).getAttribute('aria-controls')!
      expect(document.getElementById(id)).not.toBeNull()
    })
  })

  describe('many routes', () => {
    const many = (n: number) => Array.from({ length: n }, (_, i) => route(i + 1, { evaluation_order: i }))

    it('shows 5 lines and counts the rest on a button named after the video', () => {
      renderLines(many(7))
      expect(screen.getAllByRole('listitem')).toHaveLength(5)
      const more = screen.getByRole('button', { name: 'Show 2 more route results for My Video' })
      expect(more).toHaveTextContent('Show 2 more route results')
    })

    it('shows exactly 5 routes without a button', () => {
      renderLines(many(5))
      expect(screen.getAllByRole('listitem')).toHaveLength(5)
      expect(screen.queryByRole('button', { name: /more route results/ })).not.toBeInTheDocument()
    })

    it('reveals the rest in the same order and moves focus to the first revealed line', async () => {
      renderLines(many(7))
      fireEvent.click(screen.getByRole('button', { name: /Show 2 more route results/ }))
      const items = screen.getAllByRole('listitem')
      expect(items.map((li) => li.textContent?.replace('Details', ''))).toEqual(
        [1, 2, 3, 4, 5, 6, 7].map((n) => `Route ${n} (0000000${n}) — matched`),
      )
      expect(screen.queryByRole('button', { name: /more route results/ })).not.toBeInTheDocument()
      await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Details for route Route 6 (00000006)' })))
    })

    it('never reorders lines by outcome', () => {
      renderLines([
        route(1, { decision: 'matched' }),
        route(2, { decision: 'not_matched' }),
        route(3, { decision: 'skipped', skip_reason: 'disabled' }),
      ])
      expect(screen.getAllByRole('listitem').map((li) => li.textContent)).toEqual([
        'Route 1 (00000001) — matchedDetails',
        'Route 2 (00000002) — no matchDetails',
        'Route 3 (00000003) — skipped: route disabledDetails',
      ])
    })
  })

  it('renders nothing for a video with no route decisions', () => {
    const { container } = renderLines([])
    expect(container).toBeEmptyDOMElement()
  })

  it('never renders a configured value as markup', () => {
    renderLines([route(1, { decision: 'not_matched', failed_condition: { field: 'title', operator: 'contains', value: '<img src=x onerror=alert(1)>', composition: 'AND', condition_count: 1, further_failed: 0 } })])
    expect(document.querySelector('img')).toBeNull()
    expect(screen.getByRole('listitem')).toHaveTextContent('<img src=x onerror=alert(1)>')
  })
})
