import { useEffect, useState } from 'react'
import * as api from '../api/client'
import { useToast } from '../hooks/useToast'
import type { Settings, RetentionPlaylist } from '../types'

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
          Titles with similarity above this threshold are considered fuzzy duplicates. Exact identifier matches are always detected.
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
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
          Historical corpus import/export is available in this section. File picker integration to be wired to backend endpoints.
        </p>
      </div>
    </>
  )
}
