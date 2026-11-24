import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { fmt, videosText } from '../lib/stranded'
import type { StrandedBlockReason, StrandedBlockedVideo, StrandedReport as Report } from '../types'

// The category counts, the held runs and the blocked list of the stranded-video
// preview and of the acknowledgement that follows a confirm (design: "Re-queue
// Stranded Videos and the Paged Deferred Tab"; ARS-005 §8). Every count is shown,
// including a zero, so the person sees each category was checked. The categories
// are never claimed to add up to a total.

const BLOCKED_PAGE_SIZE = 50
const RUNS_SHOWN = 10

// Closed vocabulary of ARS-005 §8.1. An unknown value never shows raw: it reads
// as a plain blocked video without a sentence.
const blockCopy: Record<StrandedBlockReason, { name: string; detail: string }> = {
  unresolved_send_guard: { name: 'Unresolved send guard', detail: 'A playlist addition may already have been sent.' },
  uncertain_addition: { name: 'Uncertain playlist addition', detail: 'A playlist addition was sent and its outcome is not known.' },
  ambiguous_legacy_evidence: {
    name: 'Unclear history from an older version of YuHaul',
    detail: 'An older run may have sent an addition and left no clear result.',
  },
}

function blockText(reason: string): { name: string; detail?: string } {
  return Object.hasOwn(blockCopy, reason) ? blockCopy[reason as StrandedBlockReason] : { name: 'Blocked' }
}

interface CountRow {
  key: string
  label: string
  helper?: string
  sub?: boolean
  extra?: ReactNode
}

// The report names a held run and its video count only (ARS-005 §8.1): it does not say whether
// the run is executing, resumable or a stuck older-version run such as run 14. The line therefore
// claims nothing about what the run can do and sends the person to Run Detail, which reads the
// run's own state and offers "Mark as ended" when it applies.
function HeldRuns({ runs }: { runs: Report['not_ended']['runs'] }) {
  const [all, setAll] = useState(false)
  const shown = all ? runs : runs.slice(0, RUNS_SHOWN)
  const hidden = runs.length - shown.length
  // "Show k more runs" unmounts when it is pressed, which would drop keyboard focus to
  // <body>. Focus goes to the first run it revealed, whose line is read in full.
  const firstRevealed = useRef<HTMLLIElement>(null)
  const revealing = useRef(false)
  useLayoutEffect(() => {
    if (!all || !revealing.current) return
    revealing.current = false
    firstRevealed.current?.focus()
  }, [all])
  return (
    <>
      <ul style={{ margin: '8px 0 0', paddingLeft: 20 }}>
        {shown.map((r, i) => (
          <li key={r.run_id} ref={i === RUNS_SHOWN ? firstRevealed : undefined} tabIndex={i === RUNS_SHOWN ? -1 : undefined}>
            Run {r.run_id}: {videosText(r.videos)}. Run {r.run_id} has not ended. Open it to see whether it is still running, can be
            resumed, or can be marked as ended.{' '}
            <Link to={`/runs/${r.run_id}`}>Open run {r.run_id}</Link>
          </li>
        ))}
      </ul>
      {hidden > 0 && (
        <button
          type="button"
          className="button"
          style={{ marginTop: 8 }}
          onClick={() => {
            revealing.current = true
            setAll(true)
          }}
        >
          Show {fmt(hidden)} more {hidden === 1 ? 'run' : 'runs'}
        </button>
      )}
    </>
  )
}

export function StrandedCounts({ report, caption, firstRow }: { report: Report; caption: string; firstRow: string }) {
  const n = (v: number | undefined) => v ?? 0
  const rows: CountRow[] = [
    { key: 'requeue', label: firstRow },
    {
      key: 'blocked',
      label: `Blocked: ${fmt(report.blocked.videos)}`,
      helper: 'YuHaul will not re-queue these, because sending them again could add a video to a playlist twice.',
    },
    { key: 'blocked-guard', sub: true, label: `Unresolved send guard: ${fmt(n(report.blocked.by_reason.unresolved_send_guard))}` },
    { key: 'blocked-uncertain', sub: true, label: `Uncertain playlist addition: ${fmt(n(report.blocked.by_reason.uncertain_addition))}` },
    {
      key: 'blocked-legacy',
      sub: true,
      label: `Unclear history from an older version of YuHaul: ${fmt(n(report.blocked.by_reason.ambiguous_legacy_evidence))}`,
    },
    {
      key: 'held',
      label: `Held by a run that has not ended: ${fmt(report.not_ended.videos)}`,
      helper: 'These videos belong to a run that has not ended. Re-queue them after it ends.',
      extra: report.not_ended.runs.length > 0 ? <HeldRuns runs={report.not_ended.runs} /> : undefined,
    },
    {
      key: 'removed',
      label: `Removed by you: ${fmt(report.removed_by_user)}`,
      helper: 'You dismissed these from a queue. They stay out.',
    },
    {
      key: 'queued',
      label: `Already in a queue: ${fmt(report.already_queued.videos)}`,
      helper: 'Videos in the deferred queue are already waiting. Videos in the Errors queue stay there until you retry them.',
    },
    // already_queued.videos counts every queued video; in_errors_queue is the
    // part of it that sits in the Errors queue (ARS-005 §3.1).
    {
      key: 'queued-deferred',
      sub: true,
      label: `In the deferred queue: ${fmt(Math.max(report.already_queued.videos - report.already_queued.in_errors_queue, 0))}`,
    },
    { key: 'queued-errors', sub: true, label: `In the Errors queue: ${fmt(report.already_queued.in_errors_queue)}` },
    {
      key: 'retained',
      label: `Deferred or failed earlier, in no queue: ${fmt(report.retained_without_queue_item.videos)}`,
      helper: 'Counted only. These already have a deferred or failed result, and re-queue does not change them.',
    },
  ]
  return (
    <table className="table requeue-counts">
      <caption>{caption}</caption>
      <tbody>
        {rows.map((r) => (
          <tr key={r.key} className={r.sub ? 'requeue-counts__sub' : undefined}>
            <th scope="row">{r.label}</th>
            <td>
              {r.helper}
              {r.extra}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  )
}

function Evidence({ runId }: { runId: number | undefined }) {
  return Number.isInteger(runId) && (runId as number) > 0 ? (
    <Link to={`/runs/${runId}`}>Evidence from run {runId}</Link>
  ) : (
    <span>Run unavailable</span>
  )
}

function BlockedRow({ item }: { item: StrandedBlockedVideo }) {
  const reasons = Array.isArray(item.reasons) ? item.reasons : []
  return (
    <tr>
      {/* The response carries no title, so the row never claims one. */}
      <td data-label="Title">Video title unavailable</td>
      <td data-label="Video">
        Video ID: <Link to={`/videos/${encodeURIComponent(item.youtube_video_id)}`}>{item.youtube_video_id}</Link>
      </td>
      <td data-label="Reason">
        {reasons.map((r, i) => {
          const t = blockText(r.reason)
          return (
            <div key={i}>
              <strong>{t.name}</strong>
              {t.detail && <div>{t.detail}</div>}
            </div>
          )
        })}
      </td>
      <td data-label="Evidence">
        {reasons.map((r, i) => (
          <div key={i}>
            <Evidence runId={r.run_id} />
          </div>
        ))}
      </td>
    </tr>
  )
}

export function BlockedVideos({ blocked }: { blocked: Report['blocked'] }) {
  const [page, setPage] = useState(1)
  const [announcement, setAnnouncement] = useState('')
  const headingRef = useRef<HTMLHeadingElement>(null)
  const paged = useRef(false)
  const headingId = useId()

  const items = blocked.items
  const pages = Math.max(Math.ceil(items.length / BLOCKED_PAGE_SIZE), 1)
  const current = Math.min(page, pages)
  const start = (current - 1) * BLOCKED_PAGE_SIZE
  const shown = items.slice(start, start + BLOCKED_PAGE_SIZE)

  // After a paging action focus moves to the list heading and the new page is
  // announced once; the first render does neither.
  useEffect(() => {
    if (!paged.current) return
    headingRef.current?.focus()
    setAnnouncement(`Page ${current} of ${pages}, showing ${fmt(start + 1)}–${fmt(start + shown.length)}.`)
  }, [current, pages, start, shown.length])

  if (blocked.videos <= 0) return null

  function go(next: number) {
    paged.current = true
    setPage(next)
  }

  return (
    <section aria-labelledby={headingId}>
      <h2 id={headingId} ref={headingRef} tabIndex={-1}>
        Blocked videos ({fmt(blocked.videos)})
      </h2>
      <p>
        YuHaul will not re-queue these videos, and this screen cannot override that. Each stays blocked until its evidence is
        resolved under the existing playlist safety rules.
      </p>
      <div role="status" aria-live="polite" className="sr-only">
        {announcement}
      </div>
      {shown.length > 0 && (
        <table className="table" aria-labelledby={headingId}>
          <thead>
            <tr>
              <th scope="col">Title</th>
              <th scope="col">Video</th>
              <th scope="col">Reason</th>
              <th scope="col">Evidence</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((item) => (
              <BlockedRow key={item.video_id} item={item} />
            ))}
          </tbody>
        </table>
      )}
      {pages > 1 && (
        <nav aria-label="Blocked videos pages" style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap', marginTop: 12 }}>
          {current > 1 && (
            <button type="button" className="button" onClick={() => go(current - 1)}>
              Previous page
            </button>
          )}
          <span>
            Page {fmt(current)} of {fmt(pages)}
          </span>
          {current < pages && (
            <button type="button" className="button" onClick={() => go(current + 1)}>
              Next page
            </button>
          )}
        </nav>
      )}
    </section>
  )
}
