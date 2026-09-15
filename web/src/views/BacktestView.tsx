import { useEffect, useRef, useState } from 'react'
import * as api from '../api/client'
import { RouteEditor } from '../components/RouteEditor'
import { useToast } from '../hooks/useToast'
import type { Route, Run, BacktestComparison } from '../types'

function formatDate(iso: string): string {
  return new Date(iso).toLocaleString()
}

function ScenarioDiff({ current, proposed }: { current: Route[]; proposed: Route[] }) {
  const currentById = new Map(current.map((r) => [r.id, r]))
  const proposedById = new Map(proposed.map((r) => [r.id, r]))
  const allIds = Array.from(new Set([...current.map((r) => r.id), ...proposed.map((r) => r.id)]))
  const rows = allIds
    .map((id) => {
      const oldRoute = currentById.get(id)
      const newRoute = proposedById.get(id)
      let status: 'added' | 'removed' | 'modified' | 'unchanged' = 'unchanged'
      if (!oldRoute) status = 'added'
      else if (!newRoute) status = 'removed'
      else if (JSON.stringify(oldRoute) !== JSON.stringify(newRoute)) status = 'modified'
      return { id, oldRoute, newRoute, status }
    })
    .filter((r) => r.status !== 'unchanged')

  if (rows.length === 0) {
    return <p style={{ color: 'var(--color-text-muted)' }}>Proposed configuration matches the current active configuration.</p>
  }

  return (
    <div style={{ display: 'grid', gap: 8 }}>
      {rows.map((row) => (
        <div
          key={row.id}
          style={{
            padding: 12,
            borderRadius: 8,
            border: '1px solid var(--color-border-default)',
            background:
              row.status === 'added'
                ? 'rgba(34, 197, 94, 0.08)'
                : row.status === 'removed'
                  ? 'rgba(239, 68, 68, 0.08)'
                  : 'rgba(245, 158, 11, 0.08)',
          }}
        >
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <strong>{row.newRoute?.name ?? row.oldRoute?.name}</strong>
            <span className={`badge badge--${row.status === 'added' ? 'success' : row.status === 'removed' ? 'danger' : 'warning'}`}>
              {row.status}
            </span>
          </div>
          {row.status === 'modified' && row.oldRoute && row.newRoute && (
            <ul style={{ fontSize: '0.875rem', marginTop: 8 }}>
              {row.oldRoute.name !== row.newRoute.name && (
                <li>
                  Name: {row.oldRoute.name} → {row.newRoute.name}
                </li>
              )}
              {row.oldRoute.is_enabled !== row.newRoute.is_enabled && (
                <li>
                  Enabled: {String(row.oldRoute.is_enabled)} → {String(row.newRoute.is_enabled)}
                </li>
              )}
              {row.oldRoute.is_final !== row.newRoute.is_final && (
                <li>
                  Final: {String(row.oldRoute.is_final)} → {String(row.newRoute.is_final)}
                </li>
              )}
              {row.oldRoute.conditions.length !== row.newRoute.conditions.length && (
                <li>
                  Conditions: {row.oldRoute.conditions.length} → {row.newRoute.conditions.length}
                </li>
              )}
              {row.oldRoute.actions.length !== row.newRoute.actions.length && (
                <li>
                  Actions: {row.oldRoute.actions.length} → {row.newRoute.actions.length}
                </li>
              )}
              {row.oldRoute.exclusions.length !== row.newRoute.exclusions.length && (
                <li>
                  Exclusions: {row.oldRoute.exclusions.length} → {row.newRoute.exclusions.length}
                </li>
              )}
            </ul>
          )}
        </div>
      ))}
    </div>
  )
}

function ComparisonResult({ comparison, run }: { comparison: BacktestComparison; run?: Run }) {
  return (
    <div>
      <div style={{ display: 'grid', gap: 12, gridTemplateColumns: 'repeat(auto-fit, minmax(140px, 1fr))', marginBottom: 16 }}>
        <div className="card" style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{comparison.baseline_accepted}</div>
          <div style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>Historical accepted</div>
        </div>
        <div className="card" style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{comparison.proposed_accepted}</div>
          <div style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>Proposed accepted</div>
        </div>
        <div className="card" style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '1.5rem', fontWeight: 700, color: 'var(--color-success)' }}>{comparison.new_catches}</div>
          <div style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>New catches</div>
        </div>
        <div className="card" style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '1.5rem', fontWeight: 700, color: 'var(--color-danger)' }}>{comparison.lost_catches}</div>
          <div style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>Lost catches</div>
        </div>
        <div className="card" style={{ textAlign: 'center' }}>
          <div style={{ fontSize: '1.5rem', fontWeight: 700 }}>{comparison.net_change > 0 ? `+${comparison.net_change}` : comparison.net_change}</div>
          <div style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>Net change</div>
        </div>
      </div>
      {run && (
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
          Backtest run <strong>#{run.id}</strong> completed at {run.completed_at ? formatDate(run.completed_at) : '—'}.
        </p>
      )}
    </div>
  )
}

export function BacktestView() {
  const [mode, setMode] = useState<'hypothetical' | 'replay'>('hypothetical')
  const [inputMode, setInputMode] = useState<'editor' | 'upload'>('editor')
  const [activeRoutes, setActiveRoutes] = useState<Route[]>([])
  const [draft, setDraft] = useState<Route[]>([])
  const [uploadError, setUploadError] = useState<string | null>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [versions, setVersions] = useState<{ id: number; description?: string }[]>([])
  const [selectedVersion, setSelectedVersion] = useState<number | ''>('')
  const [dateFrom, setDateFrom] = useState('')
  const [dateTo, setDateTo] = useState('')
  const [running, setRunning] = useState(false)
  const [resultRun, setResultRun] = useState<Run | null>(null)
  const [comparison, setComparison] = useState<BacktestComparison | null>(null)
  const { addToast } = useToast()

  useEffect(() => {
    api.listRoutes().then((r) => {
      setActiveRoutes(r)
      setDraft(JSON.parse(JSON.stringify(r)))
    })
    api.listConfigVersions().then((v) => setVersions(v.map((x) => ({ id: x.id, description: x.change_description }))))
  }, [])

  function handleFileUpload(e: React.ChangeEvent<HTMLInputElement>) {
    setUploadError(null)
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      try {
        const text = String(reader.result ?? '')
        const parsed = JSON.parse(text)
        const routes: Route[] = Array.isArray(parsed.routes) ? parsed.routes : Array.isArray(parsed) ? parsed : []
        if (routes.length === 0) {
          setUploadError('File did not contain any routes. Expected { routes: [...] } or an array of routes.')
          return
        }
        setDraft(routes)
        addToast(`Loaded ${routes.length} proposed routes from file`, 'success')
      } catch (err) {
        setUploadError(err instanceof Error ? err.message : 'Failed to parse uploaded file')
      }
    }
    reader.readAsText(file)
  }

  async function run() {
    setRunning(true)
    setResultRun(null)
    setComparison(null)
    try {
      let runId: number
      let backtestComparison: BacktestComparison | undefined
      if (mode === 'hypothetical') {
        const res = await api.startBacktest({ routes: draft, duplicate_threshold: 0.5 })
        runId = res.run_id
        backtestComparison = res.comparison
      } else {
        const res = await api.startReplay(selectedVersion ? Number(selectedVersion) : undefined)
        runId = res.run_id
      }
      const runDetail = await api.getRun(runId)
      setResultRun(runDetail ?? null)
      if (backtestComparison) {
        setComparison(backtestComparison)
      } else if (runDetail) {
        // Compute a minimal comparison from the run summary when the backend
        // does not provide a pre-computed comparison.
        const baseline = await api.listRuns().then((runs) =>
          runs
            .filter((r) => r.run_type === 'scheduled' || r.run_type === 'manual')
            .reduce((sum, r) => sum + r.videos_accepted, 0)
        )
        setComparison({
          baseline_run_ids: [],
          baseline_accepted: baseline,
          proposed_accepted: runDetail.videos_accepted,
          new_catches: Math.max(0, runDetail.videos_accepted - baseline),
          lost_catches: Math.max(0, baseline - runDetail.videos_accepted),
          net_change: runDetail.videos_accepted - baseline,
        })
      }
      addToast(`${mode === 'hypothetical' ? 'Backtest' : 'Replay'} completed`, 'success')
    } catch (err) {
      addToast(err instanceof Error ? err.message : 'Failed to run backtest', 'error')
    } finally {
      setRunning(false)
    }
  }

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Backtest & Replay</h1>
      </div>

      <div style={{ display: 'flex', gap: 8, marginBottom: 16 }} role="tablist" aria-label="Backtest mode">
        <button
          className={`button ${mode === 'hypothetical' ? 'button--primary' : ''}`}
          onClick={() => setMode('hypothetical')}
          role="tab"
          aria-selected={mode === 'hypothetical'}
        >
          Hypothetical Backtest
        </button>
        <button
          className={`button ${mode === 'replay' ? 'button--primary' : ''}`}
          onClick={() => setMode('replay')}
          role="tab"
          aria-selected={mode === 'replay'}
        >
          Replay/Reprocess
        </button>
      </div>

      {mode === 'hypothetical' ? (
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Proposed configuration</h2>
          <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
            Edit routes below or upload a configuration file. Changes are not saved until applied.
          </p>
          <div style={{ display: 'flex', gap: 8, marginTop: 12, marginBottom: 12 }} role="tablist" aria-label="Configuration input method">
            <button
              className={`button ${inputMode === 'editor' ? 'button--primary' : ''}`}
              onClick={() => setInputMode('editor')}
              role="tab"
              aria-selected={inputMode === 'editor'}
            >
              Edit config
            </button>
            <button
              className={`button ${inputMode === 'upload' ? 'button--primary' : ''}`}
              onClick={() => setInputMode('upload')}
              role="tab"
              aria-selected={inputMode === 'upload'}
            >
              Upload config file
            </button>
          </div>

          {inputMode === 'editor' ? (
            draft.map((r, i) => (
              <div
                key={r.id}
                style={{ marginTop: 16, border: '1px solid var(--color-border-default)', borderRadius: 8, padding: 12 }}
              >
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
            ))
          ) : (
            <div>
              <input
                ref={fileInputRef}
                type="file"
                accept=".json,application/json"
                aria-label="Upload configuration file"
                onChange={handleFileUpload}
                style={{ marginBottom: 8 }}
              />
              {uploadError && <p style={{ color: 'var(--color-danger)' }}>{uploadError}</p>}
              <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                Upload a JSON file containing routes. Supported shapes: an array of route objects or {'{ routes: [...] }'}.
              </p>
            </div>
          )}
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

      {mode === 'hypothetical' && (
        <div className="card" style={{ marginBottom: 16 }}>
          <h2 className="section-title">Scenario diff</h2>
          <ScenarioDiff current={activeRoutes} proposed={draft} />
        </div>
      )}

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Date range</h2>
        <div style={{ display: 'grid', gap: 12, gridTemplateColumns: '1fr 1fr' }}>
          <div>
            <label className="form-label" htmlFor="from-date">
              From date
            </label>
            <input id="from-date" type="date" className="form-input" value={dateFrom} onChange={(e) => setDateFrom(e.target.value)} />
          </div>
          <div>
            <label className="form-label" htmlFor="to-date">
              To date
            </label>
            <input id="to-date" type="date" className="form-input" value={dateTo} onChange={(e) => setDateTo(e.target.value)} />
          </div>
        </div>
      </div>

      <button className="button button--primary" onClick={run} disabled={running || (mode === 'hypothetical' && draft.length === 0)}>
        {running ? 'Running...' : mode === 'hypothetical' ? 'Run backtest' : 'Run replay'}
      </button>

      {comparison && (
        <div className="card" style={{ marginTop: 16 }}>
          <h2 className="section-title">Comparison results</h2>
          <ComparisonResult comparison={comparison} run={resultRun ?? undefined} />
        </div>
      )}
    </>
  )
}
