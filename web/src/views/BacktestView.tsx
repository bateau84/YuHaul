import { useEffect, useState } from 'react'
import * as api from '../api/client'
import { RouteEditor } from '../components/RouteEditor'
import { useToast } from '../hooks/useToast'
import type { Route } from '../types'

export function BacktestView() {
  const [mode, setMode] = useState<'hypothetical' | 'replay'>('hypothetical')
  const [draft, setDraft] = useState<Route[]>([])
  const [versions, setVersions] = useState<{ id: number; description?: string }[]>([])
  const [selectedVersion, setSelectedVersion] = useState<number | ''>('')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<{ run_id: number; status: string } | null>(null)
  const { addToast } = useToast()

  useEffect(() => {
    api.listRoutes().then((r) => {
      setDraft(JSON.parse(JSON.stringify(r)))
    })
    api.listConfigVersions().then((v) => setVersions(v.map((x) => ({ id: x.id, description: x.change_description }))))
  }, [])

  async function run() {
    setRunning(true)
    let res
    if (mode === 'hypothetical') {
      res = await api.startBacktest({ routes: draft, duplicate_threshold: 0.5 })
    } else {
      res = await api.startReplay(selectedVersion ? Number(selectedVersion) : undefined)
    }
    setResult(res)
    setRunning(false)
    addToast(`${mode === 'hypothetical' ? 'Backtest' : 'Replay'} started`, 'success')
  }

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Backtest & Replay</h1>
      </div>

      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }} role="tablist">
        <button className={`button ${mode === 'hypothetical' ? 'button--primary' : ''}`} onClick={() => setMode('hypothetical')} role="tab" aria-selected={mode === 'hypothetical'}>
          Hypothetical Backtest
        </button>
        <button className={`button ${mode === 'replay' ? 'button--primary' : ''}`} onClick={() => setMode('replay')} role="tab" aria-selected={mode === 'replay'}>
          Replay/Reprocess
        </button>
      </div>

      {mode === 'hypothetical' ? (
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Proposed configuration</h2>
          <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
            Edit routes below to compare against historical observations. Changes are not saved.
          </p>
          {draft.map((r, i) => (
            <div key={r.id} style={{ marginTop: 16, border: '1px solid var(--color-border-default)', borderRadius: 8, padding: 12 }}>
              <strong>{r.name}</strong>
              <RouteEditor
                route={r}
                onSave={(updated) => {
                  const next = [...draft]
                  next[i] = updated
                  setDraft(next)
                }}
                onCancel={() => {}}
              />
            </div>
          ))}
        </div>
      ) : (
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Select configuration version</h2>
          <select
            className="form-select"
            value={selectedVersion}
            onChange={(e) => setSelectedVersion(e.target.value === '' ? '' : Number(e.target.value))}
          >
            <option value="">Current active configuration</option>
            {versions.map((v) => (
              <option key={v.id} value={v.id}>
                Version {v.id} {v.description ? `- ${v.description}` : ''}
              </option>
            ))}
          </select>
        </div>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Date range</h2>
        <div style={{ display: 'grid', gap: 12, gridTemplateColumns: '1fr 1fr' }}>
          <div>
            <label className="form-label" htmlFor="from-date">From date</label>
            <input id="from-date" type="date" className="form-input" value={dateFrom} onChange={(e) => setDateFrom(e.target.value)} />
          </div>
          <div>
            <label className="form-label" htmlFor="to-date">To date</label>
            <input id="to-date" type="date" className="form-input" value={dateTo} onChange={(e) => setDateTo(e.target.value)} />
          </div>
        </div>
      </div>

      <button className="button button--primary" onClick={run} disabled={running}>
        {running ? 'Running...' : mode === 'hypothetical' ? 'Run backtest' : 'Run replay'}
      </button>

      {result && (
        <div className="card" style={{ marginTop: 16 }}>
          <h2 className="section-title">Result</h2>
          <p>
            {mode === 'hypothetical' ? 'Backtest' : 'Replay'} run {result.run_id} started.
          </p>
        </div>
      )}
    </>
  )
}
