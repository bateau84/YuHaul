import { useCallback, useEffect, useState } from 'react'
import * as api from '../api/client'
import { ApiError } from '../api/core'
import { useToast } from '../hooks/useToast'
import type {
  Settings,
  RetentionPlaylist,
  CorpusImportJobStatus,
  CorpusImportMetadata,
  CorpusImportPreview,
  CorpusImportStatus,
} from '../types'

// Session-scoped job id for same-process status recovery. Status itself lives
// in the server process (ARS-002); this id only lets Settings re-read it after
// navigation/reload and detect a process restart (404) honestly.
const CORPUS_IMPORT_ID_KEY = 'yuhaul.corpus_import_id'
const CORPUS_STATUS_POLL_MS = 1000

const CORPUS_STATUS_LABELS: Record<CorpusImportJobStatus, string> = {
  queued: 'Import queued',
  running: 'Importing…',
  cancel_requested: 'Cancelling — stopping at the next committed chunk…',
  cancelled: 'Import cancelled',
  completed: 'Import complete',
  failed: 'Import failed',
}

// Stable ARS-002 error-reason codes to user-facing wording; unknown codes fall
// back to the code itself so no count is ever hidden.
const CORPUS_ERROR_REASON_LABELS: Record<string, string> = {
  row_read_failed: 'source row could not be read',
  empty_video_id: 'missing video ID',
  empty_fingerprint: 'title produced no fingerprint tokens',
  destination_lookup_failed: 'destination lookup failed',
  fingerprint_insert_failed: 'fingerprint insert failed',
}

const MUTED_STYLE: React.CSSProperties = { color: 'var(--color-text-muted)', fontSize: '0.875rem' }

const CORPUS_NOTICE_STYLE: React.CSSProperties = {
  border: '1px solid var(--color-border-default)',
  borderLeft: '4px solid var(--color-status-in-progress)',
  borderRadius: 4,
  padding: '8px 12px',
  margin: '0 0 12px',
  fontSize: '0.875rem',
}

const CORPUS_ERROR_STYLE: React.CSSProperties = {
  ...CORPUS_NOTICE_STYLE,
  borderLeftColor: 'var(--color-status-failed)',
}

function isTerminalImportStatus(status: CorpusImportJobStatus): boolean {
  return status === 'cancelled' || status === 'completed' || status === 'failed'
}

function isNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : 'Unexpected error'
}

// Design amendment (Settings corpus import recency): exact accepted copy for
// the null state. Null means no reliable timestamp is recorded, never that the
// corpus is empty or that no import ever occurred.
const CORPUS_RECENCY_NULL_VALUE = 'Not recorded'
const CORPUS_RECENCY_NULL_HELPER =
  'No reliable import date is available. This can occur when the database predates import tracking. Run a successful import to establish a timestamp.'

// Renders the durable RFC 3339 value in the user's local timezone as
// "<Month> <Day>, <Year> at <Time>". A malformed wire value yields null and is
// presented like the recorded-null state rather than crashing Settings.
function formatCorpusRecency(iso: string): string | null {
  const at = new Date(iso)
  if (Number.isNaN(at.getTime())) return null
  return new Intl.DateTimeFormat('en-US', {
    month: 'long',
    day: 'numeric',
    year: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
    hour12: true,
  }).format(at)
}

export function SettingsView() {
  const [settings, setSettings] = useState<Settings | null>(null)
  const [playlists, setPlaylists] = useState<RetentionPlaylist[]>([])
  const [newPlaylist, setNewPlaylist] = useState({ playlist_id: '', playlist_name: '' })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [retryKey, setRetryKey] = useState(0)
  const { addToast } = useToast()

  useEffect(() => {
    let cancelled = false
    Promise.all([api.getSettings(), api.listRetentionPlaylists()])
      .then(([s, p]) => {
        if (cancelled) return
        setSettings(s)
        setPlaylists(p)
        setLoading(false)
      })
      .catch((err) => {
        if (cancelled) return
        setError(err instanceof Error ? err.message : 'Failed to load settings')
        setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [retryKey])

  function retry() {
    setLoading(true)
    setError(null)
    setRetryKey((k) => k + 1)
  }

  // Handle OAuth callback result in the URL query string.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const auth = params.get('auth')
    if (!auth) return

    if (auth === 'connected') {
      addToast('YouTube account connected', 'success')
    } else if (auth === 'error') {
      addToast('Account connection failed. Please try again.', 'error')
    }

    // Refresh account status and strip the query parameter.
    api.getAccountStatus().then((status) => {
      setSettings((s) => (s ? { ...s, account_connected: status.connected, account_email: status.email } : s))
    })

    params.delete('auth')
    const qs = params.toString()
    const url = qs ? `${window.location.pathname}?${qs}` : window.location.pathname
    window.history.replaceState({}, '', url)
  }, [addToast])

  const [corpusFile, setCorpusFile] = useState<File | null>(null)
  const [corpusPreview, setCorpusPreview] = useState<CorpusImportPreview | null>(null)
  const [corpusPreviewing, setCorpusPreviewing] = useState(false)
  const [corpusPreviewError, setCorpusPreviewError] = useState<string | null>(null)
  const [corpusStarting, setCorpusStarting] = useState(false)
  const [corpusStartError, setCorpusStartError] = useState<string | null>(null)
  const [corpusJob, setCorpusJob] = useState<CorpusImportStatus | null>(null)
  const [corpusStatusError, setCorpusStatusError] = useState<string | null>(null)
  const [corpusCancelPending, setCorpusCancelPending] = useState(false)
  const [corpusRestartLost, setCorpusRestartLost] = useState(false)
  const [corpusRecoveryKey, setCorpusRecoveryKey] = useState(0)
  // Durable recency (ARS-002 metadata), independent of process-local job state.
  const [corpusRecency, setCorpusRecency] = useState<CorpusImportMetadata | null>(null)
  const [corpusRecencyError, setCorpusRecencyError] = useState<string | null>(null)
  // Bumped when the displayed recency must be re-read (import completion,
  // explicit retry); same key pattern as corpusRecoveryKey below.
  const [corpusRecencyKey, setCorpusRecencyKey] = useState(0)

  // Durable recency survives restarts and is read independently of the
  // in-process job-status endpoints (ARS-002). A failed read is surfaced as a
  // fetch error distinct from a recorded null; no other date is substituted.
  useEffect(() => {
    let cancelled = false
    api
      .getCorpusImportMetadata()
      .then((metadata) => {
        if (cancelled) return
        setCorpusRecency(metadata)
        setCorpusRecencyError(null)
      })
      .catch((err) => {
        if (cancelled) return
        setCorpusRecencyError(messageOf(err))
      })
    return () => {
      cancelled = true
    }
  }, [corpusRecencyKey])

  const adoptCorpusJob = useCallback((state: CorpusImportStatus) => {
    sessionStorage.setItem(CORPUS_IMPORT_ID_KEY, state.import_id)
    setCorpusJob(state)
    setCorpusStatusError(null)
    setCorpusRestartLost(false)
  }, [])

  const markCorpusStatusLost = useCallback(() => {
    // The server process restarted: job status is gone for good while rows
    // already committed to the database remain. Drop to the neutral ready
    // state without a fabricated summary.
    sessionStorage.removeItem(CORPUS_IMPORT_ID_KEY)
    setCorpusJob(null)
    setCorpusStatusError(null)
    setCorpusRestartLost(true)
  }, [])

  const applyCorpusJobUpdate = useCallback(
    (state: CorpusImportStatus) => {
      const wasActive = corpusJob !== null && !isTerminalImportStatus(corpusJob.status)
      adoptCorpusJob(state)
      if (state.status === 'completed') {
        // The durable marker commits before completed is reported, even when
        // a newly started job finishes before its first status read.
        setCorpusRecencyKey((k) => k + 1)
      }
      // SC-004 completion toast, only when this session observed the transition
      // (never when a terminal job is merely recovered on mount).
      if (wasActive && state.status === 'completed') {
        const total =
          typeof state.total_corpus_size === 'number' ? ` Total corpus: ${state.total_corpus_size.toLocaleString()} titles.` : ''
        addToast(
          `Imported ${state.inserted.toLocaleString()} new historical titles (${state.skipped.toLocaleString()} already present, ${state.errors.toLocaleString()} errors).${total}`,
          'success',
        )
      }
    },
    [corpusJob, adoptCorpusJob, addToast],
  )

  const readCorpusJobStatus = useCallback(
    async (importId: string, isActive: () => boolean): Promise<void> => {
      try {
        const state = await api.getCorpusImportStatus(importId)
        if (!isActive()) return
        applyCorpusJobUpdate(state)
      } catch (err) {
        if (!isActive()) return
        if (isNotFound(err)) markCorpusStatusLost()
        else setCorpusStatusError(`Could not read import status: ${messageOf(err)}`)
      }
    },
    [applyCorpusJobUpdate, markCorpusStatusLost],
  )

  // Recover process-local import status when Settings is entered or re-entered
  // (ARS-002). Same-process navigation/reload recovers through
  // GET /api/corpus/import; a stored job id that then 404s means the server
  // process restarted and the status was lost.
  useEffect(() => {
    let cancelled = false

    async function recover() {
      try {
        const latest = await api.getLatestCorpusImport()
        if (cancelled) return
        if (latest) {
          adoptCorpusJob(latest)
          return
        }
        const storedId = sessionStorage.getItem(CORPUS_IMPORT_ID_KEY)
        if (!storedId) return
        try {
          const state = await api.getCorpusImportStatus(storedId)
          if (!cancelled) adoptCorpusJob(state)
        } catch (err) {
          if (cancelled) return
          if (isNotFound(err)) markCorpusStatusLost()
          else setCorpusStatusError(`Could not read import status: ${messageOf(err)}`)
        }
      } catch (err) {
        if (cancelled) return
        setCorpusStatusError(`Could not check import status: ${messageOf(err)}`)
      }
    }

    void recover()
    return () => {
      cancelled = true
    }
  }, [corpusRecoveryKey, adoptCorpusJob, markCorpusStatusLost])

  // Poll the active job. Leaving Settings never cancels: the timer is cleared
  // on unmount while the server keeps importing until cancelled explicitly.
  useEffect(() => {
    if (!corpusJob || isTerminalImportStatus(corpusJob.status) || corpusStatusError) return
    let active = true
    const timer = setTimeout(() => {
      void readCorpusJobStatus(corpusJob.import_id, () => active)
    }, CORPUS_STATUS_POLL_MS)
    return () => {
      active = false
      clearTimeout(timer)
    }
  }, [corpusJob, corpusStatusError, readCorpusJobStatus])

  async function retryCorpusStatusCheck() {
    if (corpusJob) await readCorpusJobStatus(corpusJob.import_id, () => true)
    else setCorpusRecoveryKey((k) => k + 1)
  }

  async function previewCorpusFile(file: File) {
    setCorpusFile(file)
    setCorpusPreview(null)
    setCorpusPreviewError(null)
    setCorpusStartError(null)
    setCorpusPreviewing(true)
    try {
      setCorpusPreview(await api.previewCorpusImport(file))
    } catch (err) {
      setCorpusPreviewError(messageOf(err))
    } finally {
      setCorpusPreviewing(false)
    }
  }

  function onCorpusFileChange(event: React.ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    // Reset so choosing the same file again re-triggers the change event.
    event.target.value = ''
    if (!file) return
    void previewCorpusFile(file)
  }

  function resetCorpusSelection() {
    setCorpusFile(null)
    setCorpusPreview(null)
    setCorpusPreviewError(null)
    setCorpusStartError(null)
  }

  async function startCorpusImportJob() {
    if (!corpusFile) return
    setCorpusStarting(true)
    setCorpusStartError(null)
    try {
      // Confirmation re-submits the selected file; the server repeats preflight
      // rather than trusting the preview (ARS-002).
      const started = await api.startCorpusImport(corpusFile)
      sessionStorage.setItem(CORPUS_IMPORT_ID_KEY, started.import_id)
      setCorpusPreview(null)
      setCorpusFile(null)
      try {
        applyCorpusJobUpdate(await api.getCorpusImportStatus(started.import_id))
      } catch (err) {
        if (isNotFound(err)) markCorpusStatusLost()
        else setCorpusStatusError(`Import started, but its status could not be read: ${messageOf(err)}`)
      }
    } catch (err) {
      setCorpusStartError(messageOf(err))
    } finally {
      setCorpusStarting(false)
    }
  }

  async function cancelCorpusImportJob() {
    if (!corpusJob) return
    setCorpusCancelPending(true)
    try {
      applyCorpusJobUpdate(await api.cancelCorpusImport(corpusJob.import_id))
    } catch (err) {
      if (isNotFound(err)) markCorpusStatusLost()
      else setCorpusStatusError(`Could not cancel the import: ${messageOf(err)}`)
    } finally {
      setCorpusCancelPending(false)
    }
  }

  function resetCorpusImport() {
    setCorpusJob(null)
    setCorpusFile(null)
    setCorpusPreview(null)
    setCorpusPreviewError(null)
    setCorpusStartError(null)
    setCorpusStatusError(null)
  }

  async function saveSettings(patch: Partial<Settings>) {
    if (!settings) return
    try {
      const updated = await api.updateSettings({ ...settings, ...patch })
      setSettings(updated)
      addToast('Settings saved', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to save settings', 'error')
    }
  }

  async function addPlaylist(e: React.FormEvent) {
    e.preventDefault()
    if (!newPlaylist.playlist_id) return
    try {
      const created = await api.createRetentionPlaylist(newPlaylist)
      setPlaylists((prev) => [...prev, created])
      setNewPlaylist({ playlist_id: '', playlist_name: '' })
      addToast('Retention playlist added', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to add playlist', 'error')
    }
  }

  async function removePlaylist(id: number) {
    try {
      await api.deleteRetentionPlaylist(id)
      setPlaylists((prev) => prev.filter((p) => p.id !== id))
      addToast('Retention playlist removed', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to remove playlist', 'error')
    }
  }

  async function connectAccount() {
    try {
      const res = await api.initiateOAuth()
      window.location.assign(res.auth_url)
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to start OAuth flow', 'error')
    }
  }

  async function disconnect() {
    try {
      await api.disconnectAccount()
      setSettings((s) => (s ? { ...s, account_connected: false, account_email: undefined } : s))
      addToast('Account disconnected', 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to disconnect account', 'error')
    }
  }

  if (loading) return <div className="empty-state">Loading settings...</div>
  if (error) {
    return (
      <div className="card" role="alert">
        <h2 className="section-title">Error loading settings</h2>
        <p>{error}</p>
        <button className="button button--primary" onClick={retry}>Retry</button>
      </div>
    )
  }
  if (!settings) return null

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Settings</h1>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Schedule</h2>
        <label className="form-label" htmlFor="schedule">Cron expression</label>
        <input
          id="schedule"
          className="form-input"
          value={settings.schedule}
          onChange={(e) => setSettings({ ...settings, schedule: e.target.value })}
          onBlur={() => saveSettings({ schedule: settings.schedule })}
        />
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Quota</h2>
        <label className="form-label" htmlFor="quota-limit">Daily quota limit</label>
        <input
          id="quota-limit"
          type="number"
          className="form-input"
          value={settings.quota_limit}
          onChange={(e) => setSettings({ ...settings, quota_limit: Number(e.target.value) })}
          onBlur={() => saveSettings({ quota_limit: settings.quota_limit })}
        />
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Duplicate detection</h2>
        <label className="form-label" htmlFor="threshold">Similarity threshold: {settings.duplicate_threshold}</label>
        <input
          id="threshold"
          type="range"
          min={0}
          max={1}
          step={0.01}
          className="form-input"
          value={settings.duplicate_threshold}
          onChange={(e) => setSettings({ ...settings, duplicate_threshold: Number(e.target.value) })}
          onBlur={() => {
            api.setDuplicateThreshold(settings.duplicate_threshold)
            addToast('Threshold updated', 'success')
          }}
        />
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
          Only different videos in the historical corpus can be duplicate matches. Exact title matches do not depend on this threshold; fuzzy title matches use it. A video never matches itself by video ID.
        </p>
        <p className="duplicate-handling-explanation">
          Duplicate matches do not stop routing. Videos continue through eligible routes and playlist actions; exclusions, route conditions and order, final-route rules, quota and failure handling, and completed-action checks still apply.
        </p>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Account</h2>
        {settings.account_connected ? (
          <>
            <p>Connected as {settings.account_email}</p>
            <button className="button button--danger" onClick={disconnect}>Disconnect account</button>
          </>
        ) : (
          <>
            <p>Account not connected.</p>
            <button className="button button--primary" onClick={connectAccount}>Connect YouTube account</button>
          </>
        )}
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Retention playlists</h2>
        <ul style={{ listStyle: 'none', padding: 0, margin: '0 0 16px' }}>
          {playlists.map((p) => (
            <li key={p.id} style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 0', borderBottom: '1px solid var(--color-border-default)' }}>
              <span>{p.playlist_name} ({p.playlist_id})</span>
              <button className="button" onClick={() => removePlaylist(p.id)}>Remove</button>
            </li>
          ))}
        </ul>
        <form onSubmit={addPlaylist} style={{ display: 'flex', gap: 8 }}>
          <input
            className="form-input"
            placeholder="Playlist ID"
            value={newPlaylist.playlist_id}
            onChange={(e) => setNewPlaylist((p) => ({ ...p, playlist_id: e.target.value }))}
          />
          <input
            className="form-input"
            placeholder="Playlist name"
            value={newPlaylist.playlist_name}
            onChange={(e) => setNewPlaylist((p) => ({ ...p, playlist_name: e.target.value }))}
          />
          <button type="submit" className="button button--primary">Add</button>
        </form>
      </div>

      <div className="card">
        <h2 className="section-title">Data</h2>
        <CorpusImportRecency
          metadata={corpusRecency}
          error={corpusRecencyError}
          onRetry={() => setCorpusRecencyKey((k) => k + 1)}
        />
        <div style={{ fontWeight: 600, margin: '0 0 8px' }}>Historical corpus import</div>
        <p style={{ ...MUTED_STYLE, margin: '0 0 12px' }}>
          Select the prior application&apos;s SQLite database (.db or .sqlite) to preview it and append its historical titles to
          the duplicate-detection corpus. Import is append-only: video IDs already in the corpus are skipped, and no existing
          entry is replaced.
        </p>

        {corpusRestartLost && (
          <div role="status" aria-live="polite" style={CORPUS_NOTICE_STYLE}>
            Import status is unavailable because the server restarted — this import&apos;s progress and result cannot be shown.
            Rows already committed remain in the corpus. Select the same SQLite file and start the import again to retry;
            existing video IDs are skipped.
          </div>
        )}

        {corpusStatusError && (
          <div role="alert" aria-live="assertive" style={CORPUS_ERROR_STYLE}>
            <p style={{ margin: '0 0 8px' }}>{corpusStatusError}</p>
            <button type="button" className="button" onClick={() => void retryCorpusStatusCheck()}>Retry</button>
          </div>
        )}

        {corpusJob ? (
          <CorpusImportJobPanel
            job={corpusJob}
            cancelPending={corpusCancelPending}
            onCancel={() => void cancelCorpusImportJob()}
            onReset={resetCorpusImport}
          />
        ) : (
          <>
            {corpusPreviewError && (
              <div role="alert" aria-live="assertive" style={CORPUS_ERROR_STYLE}>
                <p style={{ margin: '0 0 8px' }}>{corpusPreviewError}</p>
                {corpusFile && (
                  <button type="button" className="button" onClick={() => void previewCorpusFile(corpusFile)}>
                    Retry preview
                  </button>
                )}
              </div>
            )}

            {corpusStartError && (
              <div role="alert" aria-live="assertive" style={CORPUS_ERROR_STYLE}>
                <p style={{ margin: 0 }}>{corpusStartError}</p>
              </div>
            )}

            {corpusPreviewing ? (
              <p role="status" aria-live="polite" style={{ margin: 0 }}>
                Reading preview…
              </p>
            ) : corpusPreview ? (
              <div>
                <div role="status" aria-live="polite">
                  <p style={{ margin: '0 0 4px' }}>
                    {corpusPreview.source_row_count.toLocaleString()} titles found in source database.
                  </p>
                  <p style={{ margin: '0 0 4px' }}>
                    {corpusPreview.already_present_count.toLocaleString()} video identifiers already exist in corpus (will be
                    skipped).
                  </p>
                  {corpusPreview.sample_titles.length > 0 ? (
                    <>
                      <p style={{ margin: '0 0 4px' }}>
                        First {corpusPreview.sample_titles.length} sample titles shown for confirmation:
                      </p>
                      <ul style={{ margin: '0 0 8px', paddingLeft: 20 }}>
                        {corpusPreview.sample_titles.map((title, index) => (
                          <li key={`${index}-${title}`}>{title}</li>
                        ))}
                      </ul>
                    </>
                  ) : (
                    <p style={{ margin: '0 0 4px' }}>No sample titles are available in this source.</p>
                  )}
                  <p style={{ ...MUTED_STYLE, margin: '0 0 12px' }}>
                    Preview only — nothing is written to the corpus until you start the import. The source database has no date
                    column, so no dates are reported.
                  </p>
                </div>
                <div style={{ display: 'flex', gap: 8 }}>
                  <button
                    type="button"
                    className="button button--primary"
                    onClick={() => void startCorpusImportJob()}
                    disabled={corpusStarting}
                  >
                    {corpusStarting ? 'Starting import…' : 'Start import'}
                  </button>
                  <button type="button" className="button" onClick={resetCorpusSelection}>
                    Choose a different file
                  </button>
                </div>
              </div>
            ) : (
              <div>
                <label className="form-label" htmlFor="corpus-import-file">
                  SQLite database file
                </label>
                <input
                  id="corpus-import-file"
                  type="file"
                  className="form-input"
                  accept=".db,.sqlite"
                  onChange={onCorpusFileChange}
                />
              </div>
            )}
          </>
        )}
      </div>
    </>
  )
}

interface CorpusImportJobPanelProps {
  job: CorpusImportStatus
  cancelPending: boolean
  onCancel: () => void
  onReset: () => void
}

function CorpusImportJobPanel({ job, cancelPending, onCancel, onReset }: CorpusImportJobPanelProps) {
  const terminal = isTerminalImportStatus(job.status)
  const cancelling = cancelPending || job.status === 'cancel_requested'
  const percent = job.total > 0 ? Math.round((job.processed / job.total) * 100) : 0
  const rateText = job.rate_per_second != null ? ` — ~${Math.round(job.rate_per_second).toLocaleString()} titles/sec` : ''
  const etaText =
    job.estimated_seconds_remaining != null ? ` — estimated ${job.estimated_seconds_remaining} seconds remaining` : ''
  const totalCorpusLine =
    typeof job.total_corpus_size === 'number' ? (
      <p style={{ margin: '0 0 4px' }}>
        Total corpus: {job.total_corpus_size.toLocaleString()} titles (actual destination row count after import)
      </p>
    ) : (
      <p style={{ margin: '0 0 4px' }}>Total corpus size unavailable.</p>
    )
  const errorReasons = Object.entries(job.error_counts_by_reason)

  return (
    <div>
      <div
        role={job.status === 'failed' ? 'alert' : 'status'}
        aria-live={job.status === 'failed' ? 'assertive' : 'polite'}
      >
        <p style={{ fontWeight: 600, margin: '0 0 8px' }}>{CORPUS_STATUS_LABELS[job.status]}</p>

        {job.status === 'queued' && <p style={{ ...MUTED_STYLE, margin: '0 0 4px' }}>Waiting for the import to start.</p>}

        {(job.status === 'running' || job.status === 'cancel_requested') && (
          <>
            <progress
              max={job.total > 0 ? job.total : 1}
              value={job.processed}
              aria-label="Import progress"
              style={{ width: '100%' }}
            />
            <p style={{ margin: '8px 0 4px' }}>
              {`Importing: ${job.processed.toLocaleString()} of ${job.total.toLocaleString()} titles (${percent}%)${rateText}${etaText}`}
            </p>
            <p style={{ ...MUTED_STYLE, margin: '0 0 4px' }}>
              {job.inserted.toLocaleString()} inserted · {job.skipped.toLocaleString()} already present ·{' '}
              {job.errors.toLocaleString()} failed
            </p>
          </>
        )}

        {terminal && (
          <>
            <p style={{ margin: '0 0 4px' }}>
              {job.inserted.toLocaleString()} newly inserted, {job.skipped.toLocaleString()} already present,{' '}
              {job.errors.toLocaleString()} failed.
            </p>
            {job.errors > 0 && errorReasons.length > 0 && (
              <ul style={{ margin: '0 0 8px', paddingLeft: 20 }}>
                {errorReasons.map(([reason, count]) => (
                  <li key={reason}>
                    {CORPUS_ERROR_REASON_LABELS[reason] ?? reason}: {count.toLocaleString()}
                  </li>
                ))}
              </ul>
            )}
            {totalCorpusLine}
            {job.status === 'completed' && job.errors > 0 && (
              <p style={{ ...MUTED_STYLE, margin: '0 0 4px' }}>
                Some source rows failed and were not imported; see the reasons above.
              </p>
            )}
            {job.status === 'cancelled' && (
              <p style={{ ...MUTED_STYLE, margin: '0 0 4px' }}>
                Rows committed before the cancellation remain in the corpus. Select the same SQLite file again to continue;
                existing video IDs are skipped.
              </p>
            )}
            {job.status === 'failed' && (
              <p style={{ ...MUTED_STYLE, margin: '0 0 4px' }}>
                Rows committed before the failure remain in the corpus. Select the same SQLite file again to retry; existing
                video IDs are skipped.
              </p>
            )}
          </>
        )}
      </div>

      <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
        {terminal ? (
          <button type="button" className="button button--primary" onClick={onReset}>
            Import another file
          </button>
        ) : (
          <button
            type="button"
            className="button button--danger"
            onClick={onCancel}
            disabled={cancelling}
          >
            {cancelling ? 'Cancelling…' : 'Cancel import'}
          </button>
        )}
      </div>

      {!terminal && (
        <p style={{ ...MUTED_STYLE, margin: '8px 0 0' }}>
          Leaving Settings does not cancel the import — only the Cancel button stops it.
        </p>
      )}
    </div>
  )
}

interface CorpusImportRecencyProps {
  metadata: CorpusImportMetadata | null
  error: string | null
  onRetry: () => void
}

// Durable recency of the last successful corpus import, adjacent to the
// import controls (design amendment: Settings corpus import recency display).
// Three distinct states: a formatted local-time timestamp, a recorded null
// ("Not recorded", not an error state), and a fetch error ("unavailable"),
// which is shown as such rather than being conflated with a recorded null.
function CorpusImportRecency({ metadata, error, onRetry }: CorpusImportRecencyProps) {
  if (error) {
    return (
      <div style={{ margin: '0 0 16px' }}>
        <p style={{ margin: '0 0 4px' }}>
          <span id="corpus-recency-label">Last corpus import</span>{' '}
          <span id="corpus-recency-value" aria-labelledby="corpus-recency-label" aria-describedby="corpus-recency-help">
            unavailable
          </span>
        </p>
        <p id="corpus-recency-help" style={{ ...MUTED_STYLE, margin: '0 0 8px' }} role="status">
          Could not read the last import date: {error}
        </p>
        <button type="button" className="button" onClick={onRetry}>
          Retry
        </button>
      </div>
    )
  }

  // Nothing is shown until the durable value resolves.
  if (!metadata) return null

  const timestamp = metadata.last_successful_corpus_import_at
    ? formatCorpusRecency(metadata.last_successful_corpus_import_at)
    : null

  return (
    <div style={{ margin: '0 0 16px' }}>
      <p style={{ margin: '0 0 4px' }}>
        <span id="corpus-recency-label">Last corpus import</span>{' '}
        <span
          id="corpus-recency-value"
          aria-labelledby="corpus-recency-label"
          aria-describedby={timestamp ? undefined : 'corpus-recency-help'}
        >
          {timestamp ?? CORPUS_RECENCY_NULL_VALUE}
        </span>
      </p>
      {!timestamp && (
        <p id="corpus-recency-help" style={{ ...MUTED_STYLE, margin: 0 }}>
          {CORPUS_RECENCY_NULL_HELPER}
        </p>
      )}
    </div>
  )
}
