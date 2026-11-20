import { useId, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { Ban, Check, Clock, Minus, X, type LucideIcon } from 'lucide-react'
import type { RouteDecisionSummary } from '../types'
import { routeAttribution } from '../lib/route-attribution'
import { detailsButtonName, failedConditionDetail, routeLabel, routeLine, type LineKind, type Piece } from '../lib/route-line'
import { CausalExclusion } from './CausalExclusion'

export const VISIBLE_ROUTE_LINES = 5

const icons: Record<LineKind, LucideIcon> = {
  matched: Check,
  no_match: X,
  excluded: Ban,
  skipped: Minus,
  deferred: Clock,
  unknown: Minus,
}

// Symbols (=, <, >, –) are hidden from assistive technology and replaced by
// the spoken form, which is read instead.
function Text({ piece }: { piece: Piece }) {
  if (piece.visible === piece.spoken) return <>{piece.visible}</>
  return <>
    <span aria-hidden="true">{piece.visible}</span>
    <span className="sr-only">{piece.spoken}</span>
  </>
}

function ConditionDetails({ decision }: { decision: RouteDecisionSummary }) {
  const c = decision.failed_condition
  if (!c) return <p>Condition details are in the full decision detail.</p>
  if (c.composition === 'OR') {
    return <p>None of {c.condition_count} conditions matched. Condition details are in the full decision detail.</p>
  }
  return <>
    <p>{failedConditionDetail(c)}</p>
    {c.further_failed > 0 && <p>{c.further_failed} more {c.further_failed === 1 ? 'condition was' : 'conditions were'} not met. Condition details are in the full decision detail.</p>}
  </>
}

function RouteLineItem({ decision, runId, videoId, simulation }: {
  decision: RouteDecisionSummary
  runId: number | string
  videoId: string
  simulation: boolean
}) {
  const [open, setOpen] = useState(false)
  const regionId = useId()
  const line = routeLine(decision)
  const Icon = icons[line.kind]
  return (
    <li style={{ padding: '4px 0', overflowWrap: 'anywhere' }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'baseline', gap: '4px 8px' }}>
        <span>
          <Icon size={14} aria-hidden="true" style={{ verticalAlign: 'text-bottom', marginRight: 4 }} />
          <span>{routeLabel(decision)}</span>
          {' — '}
          <span>{line.outcome}</span>
          {line.reason && <>: <Text piece={line.reason} /></>}
        </span>
        <button
          type="button"
          className="button"
          aria-expanded={open}
          aria-controls={regionId}
          aria-label={detailsButtonName(decision)}
          style={{ minHeight: 24, minWidth: 24 }}
          onClick={() => setOpen((o) => !o)}
        >
          Details
        </button>
      </div>
      <div id={regionId} hidden={!open} style={{ margin: '4px 0 0 18px' }}>
        {open && <>
          {simulation && <p>Simulated result. No playlist was changed.</p>}
          <p>{routeAttribution(decision)}</p>
          {decision.route_name_source === 'current_local' && decision.route_name && (
            <p>This is the current route name, not necessarily the name when this run was evaluated.</p>
          )}
          {decision.decision === 'skipped' && decision.skip_reason === 'exclusion' && <CausalExclusion decision={decision} />}
          {decision.decision === 'not_matched' && <ConditionDetails decision={decision} />}
          <p><Link to={`/runs/${runId}/video/${videoId}`}>Open full decision detail</Link></p>
        </>}
      </div>
    </li>
  )
}

// One compact line per route, in recorded evaluation order. The first five are
// shown; the rest are one button away and are counted so the list never
// implies complete coverage.
export function RouteResults({ decisions, runId, videoId, videoTitle, simulation }: {
  decisions: RouteDecisionSummary[]
  runId: number | string
  videoId: string
  videoTitle: string
  simulation: boolean
}) {
  const headingId = useId()
  const [showAll, setShowAll] = useState(false)
  const listRef = useRef<HTMLUListElement>(null)
  if (decisions.length === 0) return null
  const hidden = showAll ? 0 : Math.max(0, decisions.length - VISIBLE_ROUTE_LINES)
  const shown = hidden ? decisions.slice(0, VISIBLE_ROUTE_LINES) : decisions

  // The button disappears when pressed; hand focus to the first revealed line
  // instead of dropping it to the page.
  const reveal = () => {
    setShowAll(true)
    requestAnimationFrame(() => {
      listRef.current?.querySelectorAll<HTMLButtonElement>('button[aria-expanded]')[VISIBLE_ROUTE_LINES]?.focus()
    })
  }

  return (
    <div style={{ marginTop: 8 }}>
      <p id={headingId} style={{ margin: '0 0 4px', fontWeight: 600 }}>
        {simulation ? 'Simulated route results' : 'Route results'}
      </p>
      <ul ref={listRef} aria-labelledby={headingId} style={{ listStyle: 'none', margin: 0, padding: 0 }}>
        {shown.map((d) => (
          <RouteLineItem key={d.route_id} decision={d} runId={runId} videoId={videoId} simulation={simulation} />
        ))}
      </ul>
      {hidden > 0 && (
        <button
          type="button"
          className="button"
          aria-label={`Show ${hidden} more route results for ${videoTitle}`}
          style={{ minHeight: 24 }}
          onClick={reveal}
        >
          Show {hidden} more route results
        </button>
      )}
    </div>
  )
}
