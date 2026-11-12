import { useCallback, useEffect, useId, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { searchVideos } from '../api/client'
import { ApiError } from '../api/core'
import { OutcomeBadge, QueueShort, RunLink } from '../components/VideoParts'
import { channelLabel, echo, formatWhen, mutedStyle, titleUnavailableLabel } from '../lib/video-copy'
import type { VideoSearchPage, VideoSearchResponse, VideoSummary } from '../types'

// Copy comes verbatim from the design section "Videos — Find a Video by Title
// or ID". The permanent notice is static text: no request, no dismissal.
export const VIDEOS_NOTICE =
  "Name search finds only videos whose titles YuHaul still holds. Titles are held for a limited time: 30 days without refresh. Searching by video ID or YouTube link always works, even when the title is no longer held. Search reads only YuHaul's own records and never asks YouTube."
export const VIDEOS_HINT = 'For example: lofi beats, dQw4w9WgXcQ, or https://youtu.be/dQw4w9WgXcQ'

const EMPTY_MESSAGE = 'Type part of a video title, a video ID, or paste a YouTube link.'
const msgTooShort = 'Type at least 3 characters, or a video ID.'
const msgTooLong = 'The search text is too long. Use at most 200 characters.'
const msgLink =
  'This link does not contain a video ID YuHaul can use. Paste a youtube.com/watch?v=…, youtu.be/… or youtube.com/shorts/… link, or the 11-character video ID.'
const msgFailed = 'Could not search. Nothing was changed. Try again.'
// Not in the design table: the server rejects a stale or foreign cursor or
// page (ARS-001 §7.5) and the SPA "tells the user to start the search again".
const msgStale = 'This page of results is no longer valid. Start the search again.'
const msgInvalidText = 'The search text contains characters YuHaul cannot read. Change the text and search again.'

type Outcome =
  | { phase: 'idle' }
  | { phase: 'pending' }
  | { phase: 'done'; response: VideoSearchResponse }
  | { phase: 'failed'; stale: boolean }

function parsePage(raw: string | null): number | undefined {
  if (raw === null) return undefined
  return /^\d+$/.test(raw) ? Number(raw) : Number.NaN
}

function ResultItem({ video, from }: { video: VideoSummary; from: string }) {
  const outcome = video.latest_outcome
  return (
    <li className="card" style={{ marginBottom: 12, display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div>
        <Link to={`/videos/${video.video_id}`} state={{ from }} style={{ fontWeight: 600, display: 'inline-block', minHeight: 24 }}>
          {video.title.text ?? titleUnavailableLabel(video.title.availability)}
        </Link>{' '}
        <span style={mutedStyle}>Current/local information</span>
      </div>
      <div style={mutedStyle}>Video ID: {video.video_id}</div>
      <div>{channelLabel(video.channel)}</div>
      <div>
        {outcome.state === 'committed' ? (
          <>
            <OutcomeBadge outcome={outcome.outcome} />{' '}
            {outcome.run_id !== null && <RunLink id={outcome.run_id} />}
            {formatWhen(outcome.committed_at) && <span style={mutedStyle}> · {formatWhen(outcome.committed_at)}</span>}
          </>
        ) : outcome.state === 'simulation_only' ? (
          'Simulation only'
        ) : outcome.state === 'imported_reference' ? (
          'Imported reference'
        ) : (
          'Not yet evaluated'
        )}
      </div>
      <div style={mutedStyle}>
        <QueueShort state={video.queue_state} />
      </div>
    </li>
  )
}

export function VideosView() {
  const [params] = useSearchParams()
  const routerNavigate = useNavigate()
  // Keep a stable reference so a location change alone never re-runs a search.
  const navigateRef = useRef(routerNavigate)
  useEffect(() => {
    navigateRef.current = routerNavigate
  }, [routerNavigate])
  const navigate = useCallback((to: string, options?: { replace?: boolean }) => navigateRef.current(to, options), [])
  const q = params.get('q') ?? ''
  const pageParam = params.get('page')
  const [text, setText] = useState(q)
  const [outcome, setOutcome] = useState<Outcome>({ phase: 'idle' })
  const [nonce, setNonce] = useState(0)
  const [announce, setAnnounce] = useState('')
  const inputId = useId()
  const hintId = `${inputId}-hint`
  const noticeId = 'videos-notice'
  const messageId = `${inputId}-message`
  const headingRef = useRef<HTMLHeadingElement>(null)
  // Cursor of the page the user just asked for by Next/Previous. It lives in
  // memory only and is never put in the URL (ARS-001 §8.3, §8.4).
  const pendingCursor = useRef<{ page: number; cursor: string } | null>(null)
  const focusHeading = useRef(false)

  useEffect(() => {
    document.title = 'Videos — YuHaul'
  }, [])

  // Keep the box in step with Back/Forward to another search.
  useEffect(() => {
    setText(q)
  }, [q])

  const trimmedQ = q.trim()
  useEffect(() => {
    if (trimmedQ === '') {
      setOutcome({ phase: 'idle' })
      return
    }
    let cancelled = false
    setOutcome({ phase: 'pending' })
    const page = parsePage(pageParam)
    const pending = pendingCursor.current
    pendingCursor.current = null
    const query =
      pending && pending.page === page ? { cursor: pending.cursor } : page !== undefined ? { page } : {}
    searchVideos(trimmedQ, query)
      .then((response) => {
        if (cancelled) return
        if (response.kind === 'video') {
          navigate(`/videos/${response.video_id}`, { replace: true })
          return
        }
        setOutcome({ phase: 'done', response })
        if (response.kind === 'results' || (response.kind === 'id_not_found' && response.results)) {
          const r = response.results!
          const n = r.items.length
          setAnnounce(
            focusHeading.current && r.first_position !== null && r.last_position !== null
              ? `Page ${r.number}, showing ${r.first_position}–${r.last_position}.`
              : n === 1
                ? '1 video on this page.'
                : `${n} videos on this page.`
          )
        }
      })
      .catch((err: unknown) => {
        if (cancelled) return
        const stale = err instanceof ApiError && err.status === 400 && (err.message === 'invalid_cursor' || err.message === 'invalid_page')
        setOutcome({ phase: 'failed', stale })
      })
    return () => {
      cancelled = true
    }
  }, [trimmedQ, pageParam, nonce, navigate])

  // After paging, focus moves to the results heading (SC 2.4.3).
  useEffect(() => {
    if (outcome.phase === 'done' && focusHeading.current) {
      focusHeading.current = false
      headingRef.current?.focus()
    }
  }, [outcome])

  const submit = useCallback(
    (e: FormEvent) => {
      e.preventDefault()
      const t = text.trim()
      pendingCursor.current = null
      focusHeading.current = false
      if (t === '') {
        navigate('/videos')
        return
      }
      const next = `/videos?${new URLSearchParams({ q: t }).toString()}`
      if (t === q && pageParam === null) setNonce((n) => n + 1)
      else navigate(next)
    },
    [text, q, pageParam, navigate]
  )

  const goToPage = (target: number, cursor: string | null) => {
    if (cursor) pendingCursor.current = { page: target, cursor }
    focusHeading.current = true
    navigate(`/videos?${new URLSearchParams({ q: trimmedQ, page: String(target) }).toString()}`)
  }
  const goToFirst = () => {
    focusHeading.current = true
    navigate(`/videos?${new URLSearchParams({ q: trimmedQ }).toString()}`)
  }

  const pending = outcome.phase === 'pending'
  const response = outcome.phase === 'done' ? outcome.response : null

  let message: { text: string; role?: 'alert' | 'status'; error?: boolean } | null = null
  if (outcome.phase === 'idle') message = { text: EMPTY_MESSAGE }
  else if (outcome.phase === 'failed') message = { text: outcome.stale ? msgStale : msgFailed, role: 'alert', error: true }
  else if (response) {
    switch (response.kind) {
      case 'empty':
        message = { text: EMPTY_MESSAGE }
        break
      case 'too_short':
        message = { text: msgTooShort, role: 'alert', error: true }
        break
      case 'too_long':
        message = { text: msgTooLong, role: 'alert', error: true }
        break
      case 'unusable_link':
        message = { text: msgLink, role: 'alert', error: true }
        break
      case 'invalid_text':
        message = { text: msgInvalidText, role: 'alert', error: true }
        break
      case 'id_not_found':
        message = {
          text: response.from_link
            ? `YuHaul knows no video with the ID ${response.video_id}, taken from your link. Video IDs are case-sensitive. YuHaul searched only its own records and did not ask YouTube.`
            : `YuHaul knows no video with the ID ${response.video_id}. Video IDs are case-sensitive, so check the letters. YuHaul searched only its own records and did not ask YouTube.`,
          role: 'status',
        }
        break
      case 'results':
        if (response.results.total === 0) {
          message = {
            text: `No video with a held title contains “${echo(trimmedQ)}”. Videos whose titles are no longer held can still be found by video ID or YouTube link.`,
            role: 'status',
          }
        } else if (response.results.items.length === 0) {
          message = {
            text: 'This page has no results. Held titles may have changed since this search began. Go to page 1.',
            role: 'status',
          }
        }
        break
    }
  }

  const page: VideoSearchPage | null =
    response && (response.kind === 'results' || response.kind === 'id_not_found') && response.results ? response.results : null
  const emptyPage = page !== null && page.total > 0 && page.items.length === 0
  const showList = page !== null && page.items.length > 0
  const from = `/videos${trimmedQ ? `?${new URLSearchParams({ q: trimmedQ, ...(pageParam ? { page: pageParam } : {}) }).toString()}` : ''}`

  return (
    <div>
      <h1 className="page-title" tabIndex={-1}>
        Videos
      </h1>
      <form role="search" onSubmit={submit} style={{ marginBottom: 12 }} noValidate>
        <label className="form-label" htmlFor={inputId}>
          Search by video title, video ID or YouTube link
        </label>
        <p id={hintId} style={{ ...mutedStyle, margin: '0 0 4px' }}>
          {VIDEOS_HINT}
        </p>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
          <input
            id={inputId}
            type="search"
            className="form-input"
            style={{ flex: '1 1 240px', width: 'auto' }}
            value={text}
            onChange={(e) => setText(e.target.value)}
            enterKeyHint="search"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            aria-invalid={message?.error ? true : undefined}
            aria-describedby={[hintId, noticeId, message ? messageId : null].filter(Boolean).join(' ')}
          />
          <button type="submit" className="button" disabled={pending} style={{ minHeight: 44 }}>
            {pending ? 'Searching…' : 'Search'}
          </button>
        </div>
      </form>

      <p id={noticeId} style={{ margin: '0 0 16px' }}>
        {VIDEOS_NOTICE}
      </p>

      <div id={messageId}>
        {message && (
          <p role={message.role} className={message.error ? 'form-error' : undefined} style={{ margin: '0 0 12px' }}>
            {message.text}
          </p>
        )}
        {outcome.phase === 'failed' && !outcome.stale && (
          <button type="button" className="button" onClick={() => setNonce((n) => n + 1)}>
            Retry search
          </button>
        )}
        {emptyPage && (
          <button type="button" className="button" onClick={goToFirst}>
            Go to page 1
          </button>
        )}
      </div>

      {/* One polite announcement per search or page change. */}
      <div role="status" className="sr-only">
        {announce}
      </div>

      {showList && page && (
        <section aria-labelledby={`${inputId}-results`}>
          <h2 id={`${inputId}-results`} className="section-title" tabIndex={-1} ref={headingRef}>
            Videos whose titles contain “{echo(trimmedQ)}”
          </h2>
          <p style={{ margin: '0 0 12px' }}>
            Showing {page.first_position}–{page.last_position} of {page.total}. Newest discovery first.
          </p>
          <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
            {page.items.map((v) => (
              <ResultItem key={v.video_id} video={v} from={from} />
            ))}
          </ul>
          <nav aria-label="Search result pages" style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 12 }}>
            {page.number > 1 && (
              <button type="button" className="button" style={{ minHeight: 44 }} onClick={() => goToPage(page.number - 1, page.prev_cursor)}>
                Previous page
              </button>
            )}
            <span>Page {page.number}</span>
            {page.next_cursor && (
              <button type="button" className="button" style={{ minHeight: 44 }} onClick={() => goToPage(page.number + 1, page.next_cursor)}>
                Next page
              </button>
            )}
          </nav>
        </section>
      )}
    </div>
  )
}
