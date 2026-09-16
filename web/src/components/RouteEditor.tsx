import { useCallback, useEffect, useMemo, useState } from 'react'
import { Trash2, Plus, GripVertical, ArrowUp, ArrowDown, Check, X, Loader2, AlertCircle } from 'lucide-react'
import { DeleteConfirmDialog } from './DeleteConfirmDialog'
import { listPlaylists } from '../api/client'
import { blankRoute, normalizeRoute } from '../lib/route'
import type { Route, RouteCondition, RouteAction, Composition, ConditionField, ConditionOperator, ExclusionType, Playlist } from '../types'

const fields: { value: ConditionField; label: string }[] = [
  { value: 'title', label: 'Title' },
  { value: 'channel_id', label: 'Channel ID' },
  { value: 'channel_name', label: 'Channel name' },
  { value: 'description', label: 'Description' },
  { value: 'duration_seconds', label: 'Duration (seconds)' },
]

const textOperators: { value: ConditionOperator; label: string }[] = [
  { value: 'contains', label: 'contains' },
  { value: 'equals', label: 'equals' },
  { value: 'starts_with', label: 'starts with' },
  { value: 'ends_with', label: 'ends with' },
  { value: 'matches_regex', label: 'matches regex' },
]

const numericOperators: { value: ConditionOperator; label: string }[] = [
  { value: 'equals', label: 'equals' },
  { value: 'greater_than', label: 'greater than' },
  { value: 'less_than', label: 'less than' },
  { value: 'between', label: 'between' },
]

function operatorsForField(field: ConditionField) {
  return field === 'duration_seconds' ? numericOperators : textOperators
}

interface RouteEditorProps {
  route?: Route
  existingRoutes?: Route[]
  onSave: (route: Route) => void
  onCancel: () => void
  onDelete?: () => void
}

export function RouteEditor({ route, existingRoutes = [], onSave, onCancel, onDelete }: RouteEditorProps) {
  const [form, setForm] = useState<Route>(() => (route ? normalizeRoute(route) : blankRoute()))
  const [initialForm] = useState(() => JSON.parse(JSON.stringify(route ? normalizeRoute(route) : blankRoute())))
  const isDirty = useMemo(() => JSON.stringify(form) !== JSON.stringify(initialForm), [form, initialForm])
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [showDeleteDialog, setShowDeleteDialog] = useState(false)
  const [playlists, setPlaylists] = useState<Playlist[]>([])
  const [playlistsLoading, setPlaylistsLoading] = useState(true)
  const [playlistsError, setPlaylistsError] = useState<string | null>(null)

  const loadPlaylists = useCallback(async () => {
    setPlaylistsLoading(true)
    setPlaylistsError(null)
    try {
      const data = await listPlaylists()
      setPlaylists(data)
    } catch (err) {
      setPlaylistsError(err instanceof Error ? err.message : 'Failed to load playlists')
    } finally {
      setPlaylistsLoading(false)
    }
  }, [])

  /* eslint-disable react/set-state-in-effect */
  useEffect(() => {
    loadPlaylists()
  }, [loadPlaylists])
  /* eslint-enable react/set-state-in-effect */

  useEffect(() => {
    function onBeforeUnload(e: BeforeUnloadEvent) {
      if (!isDirty) return
      e.preventDefault()
      e.returnValue = ''
    }
    window.addEventListener('beforeunload', onBeforeUnload)
    return () => window.removeEventListener('beforeunload', onBeforeUnload)
  }, [isDirty])

  function handleCancel() {
    if (isDirty) {
      const confirmed = window.confirm('You have unsaved changes. Are you sure you want to leave?')
      if (!confirmed) return
    }
    onCancel()
  }

  function validate(): boolean {
    const next: Record<string, string> = {}
    const nameTrimmed = form.name.trim()
    if (!nameTrimmed) {
      next.name = 'Route name is required'
    } else if (nameTrimmed.length > 100) {
      next.name = 'Route name must be 100 characters or fewer'
    } else if (
      existingRoutes.some(
        (r) => r.id !== form.id && r.name.trim().toLowerCase() === nameTrimmed.toLowerCase()
      )
    ) {
      next.name = 'A route with this name already exists'
    }

    if (form.conditions.length === 0) {
      next.conditions = 'Add at least one condition'
    } else {
      form.conditions.forEach((c, i) => {
        if (!c.field) next[`condition-${i}-field`] = 'Select a field'
        if (!c.operator) next[`condition-${i}-operator`] = 'Select an operator'
        if (!c.value.trim()) {
          next[`condition-${i}-value`] = 'Enter a value'
        } else if (c.operator === 'between') {
          const parts = c.value.split(',')
          if (parts.length !== 2 || parts.some((p) => p.trim() === '' || Number.isNaN(Number(p.trim())))) {
            next[`condition-${i}-value`] = 'Enter two numbers separated by a comma'
          }
        } else if (c.field === 'duration_seconds') {
          const num = Number(c.value.trim())
          if (Number.isNaN(num)) {
            next[`condition-${i}-value`] = 'Enter a number'
          } else if (num < 0) {
            next[`condition-${i}-value`] = 'Enter a non-negative number'
          }
        }
      })
    }

    if (form.actions.length === 0) {
      next.actions = 'Add at least one action'
    } else {
      form.actions.forEach((a, i) => {
        if (!a.target_playlist_id.trim()) next[`action-${i}-playlist`] = 'Select a playlist'
      })
    }

    const minDuration = form.exclusions.find((e) => e.exclusion_type === 'duration_min')?.exclusion_value
    const maxDuration = form.exclusions.find((e) => e.exclusion_type === 'duration_max')?.exclusion_value
    if (minDuration && maxDuration && Number(minDuration) > Number(maxDuration)) {
      next.duration = 'Minimum duration must be less than or equal to maximum'
    }

    setErrors(next)
    return Object.keys(next).length === 0
  }

  useEffect(() => {
    const keys = Object.keys(errors)
    if (keys.length === 0) return
    const first = keys[0]
    const el = document.getElementById(
      first === 'name'
        ? 'route-name'
        : first === 'conditions'
          ? 'add-condition'
          : first === 'actions'
            ? 'add-action'
            : first === 'duration'
              ? 'duration-min'
              : first
    )
    // For between-operator value errors the rendered inputs use a `-min`
    // suffix; focus the minimum input so the error is announced.
    if (el == null) {
      const betweenMatch = /^condition-(\d+)-value$/.exec(first)
      if (betweenMatch) {
        const betweenInput = document.getElementById(`condition-${betweenMatch[1]}-value-min`)
        betweenInput?.focus()
        return
      }
    }
    el?.focus()
  }, [errors])

  function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    onSave(form)
  }

  function normalizeConditionValue(field: ConditionField, operator: ConditionOperator, currentValue: string): string {
    if (operator === 'between') {
      const parts = currentValue.split(',')
      if (parts.length === 2) return currentValue
      return currentValue.includes(',') ? currentValue : `${currentValue},`
    }
    // Leaving the between operator can leave a trailing comma (e.g. "180,").
    // For numeric fields, keep only the first numeric segment.
    if (field === 'duration_seconds') {
      const first = currentValue.split(',')[0].trim()
      if (first !== '' && !Number.isNaN(Number(first))) return first
      return ''
    }
    return currentValue
  }

  function defaultOperatorForField(field: ConditionField): ConditionOperator {
    return field === 'duration_seconds' ? 'greater_than' : 'contains'
  }

  function updateCondition(index: number, patch: Partial<RouteCondition>) {
    setForm((f) => {
      const conditions = [...f.conditions]
      const current = conditions[index]
      const next = { ...current, ...patch }

      if (patch.field && patch.field !== current.field) {
        const valid = operatorsForField(patch.field).map((o) => o.value)
        if (!valid.includes(next.operator)) {
          next.operator = defaultOperatorForField(patch.field)
          next.value = ''
        }
      }

      if (patch.operator && patch.operator !== current.operator) {
        next.value = normalizeConditionValue(current.field, patch.operator, current.value)
      }

      conditions[index] = next
      return { ...f, conditions }
    })
  }

  function updateBetweenValue(index: number, part: 'min' | 'max', value: string) {
    setForm((f) => {
      const conditions = [...f.conditions]
      const current = conditions[index]
      const parts = current.value.split(',')
      const min = part === 'min' ? value : parts[0] ?? ''
      const max = part === 'max' ? value : parts[1] ?? ''
      conditions[index] = { ...current, value: `${min},${max}` }
      return { ...f, conditions }
    })
  }

  function addCondition() {
    setForm((f) => ({
      ...f,
      conditions: [
        ...f.conditions,
        { condition_index: f.conditions.length, field: 'title', operator: 'contains', value: '' },
      ],
    }))
  }

  function removeCondition(index: number) {
    setForm((f) => ({
      ...f,
      conditions: f.conditions.filter((_, i) => i !== index).map((c, i) => ({ ...c, condition_index: i })),
    }))
  }

  function addAction() {
    setForm((f) => ({
      ...f,
      actions: [
        ...f.actions,
        { action_type: 'playlist_insert', target_playlist_id: '', action_index: f.actions.length },
      ],
    }))
  }

  function updateAction(index: number, patch: Partial<RouteAction>) {
    setForm((f) => {
      const actions = [...f.actions]
      actions[index] = { ...actions[index], ...patch }
      return { ...f, actions }
    })
  }

  function removeAction(index: number) {
    setForm((f) => ({
      ...f,
      actions: f.actions.filter((_, i) => i !== index).map((a, i) => ({ ...a, action_index: i })),
    }))
  }

  function addExclusion(type: ExclusionType) {
    setForm((f) => ({
      ...f,
      exclusions: [...f.exclusions, { exclusion_type: type, exclusion_value: '' }],
    }))
  }

  function updateExclusion(index: number, value: string) {
    setForm((f) => {
      const exclusions = [...f.exclusions]
      exclusions[index] = { ...exclusions[index], exclusion_value: value }
      return { ...f, exclusions }
    })
  }

  function removeExclusion(index: number) {
    setForm((f) => ({ ...f, exclusions: f.exclusions.filter((_, i) => i !== index) }))
  }

  return (
    <form onSubmit={submit}>
      <div className="card" style={{ marginBottom: 16 }}>
        <label className="form-label" htmlFor="route-name">
          Route name
        </label>
        <input
          id="route-name"
          className="form-input"
          value={form.name}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
          aria-invalid={!!errors.name}
          aria-describedby={errors.name ? 'route-name-error' : 'route-name-count'}
          aria-required="true"
          autoFocus
        />
        <div className="char-count" id="route-name-count" aria-live="polite">
          {form.name.length}/100 characters
        </div>
        {errors.name && (
          <div id="route-name-error" className="form-error" aria-live="polite">
            {errors.name}
          </div>
        )}

        <label className="form-label" htmlFor="route-description" style={{ marginTop: 16 }}>
          Description
        </label>
        <input
          id="route-description"
          className="form-input"
          value={form.description}
          onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
        />
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
          <h2 className="section-title">Conditions</h2>
          <select
            className="form-select"
            style={{ width: 'auto' }}
            value={form.condition_composition}
            onChange={(e) => setForm((f) => ({ ...f, condition_composition: e.target.value as Composition }))}
            aria-label="Condition composition"
          >
            <option value="AND">All conditions must match (AND)</option>
            <option value="OR">Any condition may match (OR)</option>
          </select>
        </div>
        {form.conditions.map((c, i) => (
          <div key={i}>
            <div className="field-row" style={{ alignItems: 'end' }}>
              <select
                id={`condition-${i}-field`}
                className="form-select"
                value={c.field}
                onChange={(e) => updateCondition(i, { field: e.target.value as ConditionField })}
                aria-label={`Condition ${i + 1} field`}
                aria-invalid={!!errors[`condition-${i}-field`]}
                aria-describedby={errors[`condition-${i}-field`] ? `condition-${i}-field-error` : undefined}
              >
                {fields.map((f) => (
                  <option key={f.value} value={f.value}>
                    {f.label}
                  </option>
                ))}
              </select>
              <select
                id={`condition-${i}-operator`}
                className="form-select"
                value={c.operator}
                onChange={(e) => updateCondition(i, { operator: e.target.value as ConditionOperator })}
                aria-label={`Condition ${i + 1} operator`}
                aria-invalid={!!errors[`condition-${i}-operator`]}
                aria-describedby={errors[`condition-${i}-operator`] ? `condition-${i}-operator-error` : undefined}
              >
                {operatorsForField(c.field).map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
              {c.operator === 'between' ? (
                <div className="field-row" style={{ gap: 8, flex: 1 }}>
                  <input
                    id={`condition-${i}-value-min`}
                    className="form-input"
                    type="number"
                    value={c.value.split(',')[0] ?? ''}
                    onChange={(e) => updateBetweenValue(i, 'min', e.target.value)}
                    placeholder="Min"
                    aria-label={`Condition ${i + 1} minimum value`}
                    aria-invalid={!!errors[`condition-${i}-value`]}
                    aria-describedby={errors[`condition-${i}-value`] ? `condition-${i}-value-error` : undefined}
                  />
                  <input
                    id={`condition-${i}-value-max`}
                    className="form-input"
                    type="number"
                    value={c.value.split(',')[1] ?? ''}
                    onChange={(e) => updateBetweenValue(i, 'max', e.target.value)}
                    placeholder="Max"
                    aria-label={`Condition ${i + 1} maximum value`}
                    aria-invalid={!!errors[`condition-${i}-value`]}
                    aria-describedby={errors[`condition-${i}-value`] ? `condition-${i}-value-error` : undefined}
                  />
                </div>
              ) : (
                <input
                  id={`condition-${i}-value`}
                  className="form-input"
                  type={c.field === 'duration_seconds' ? 'number' : 'text'}
                  min={c.field === 'duration_seconds' ? 0 : undefined}
                  value={c.value}
                  onChange={(e) => updateCondition(i, { value: e.target.value })}
                  placeholder="Value"
                  aria-label={`Condition ${i + 1} value`}
                  aria-invalid={!!errors[`condition-${i}-value`]}
                  aria-describedby={errors[`condition-${i}-value`] ? `condition-${i}-value-error` : undefined}
                  style={c.operator === 'matches_regex' ? { fontFamily: 'monospace' } : undefined}
                />
              )}
              <button type="button" className="icon-button" onClick={() => removeCondition(i)} aria-label={`Remove condition ${i + 1}`}>
                <Trash2 size={16} />
              </button>
            </div>
            {(['field', 'operator', 'value'] as const).map((k) =>
              errors[`condition-${i}-${k}`] ? (
                <div key={k} id={`condition-${i}-${k}-error`} className="form-error" aria-live="polite">
                  {errors[`condition-${i}-${k}`]}
                </div>
              ) : null
            )}
          </div>
        ))}
        {errors.conditions && (
          <div id="conditions-error" className="form-error" aria-live="polite">
            {errors.conditions}
          </div>
        )}
        <button id="add-condition" type="button" className="button" onClick={addCondition}>
          <Plus size={16} /> Add condition
        </button>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Exclusions</h2>
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
          Excluded videos skip this route but remain eligible for other routes.
        </p>
        {['channel', 'video', 'title', 'word'].map((type) => (
          <div key={type} style={{ marginTop: 12 }}>
            <strong style={{ textTransform: 'capitalize' }}>{type.replace('_', ' ')}</strong>
            <ul style={{ listStyle: 'none', margin: '8px 0', padding: 0, display: 'flex', flexWrap: 'wrap', gap: 8 }}>
              {form.exclusions
                .filter((e) => e.exclusion_type === type)
                .map((e) => {
                  const globalIdx = form.exclusions.indexOf(e)
                  return (
                    <li key={globalIdx} className="badge badge--not-evaluated">
                      {e.exclusion_value}
                      <button
                        type="button"
                        className="icon-button"
                        onClick={() => removeExclusion(globalIdx)}
                        aria-label={`Remove ${e.exclusion_value}`}
                      >
                        <Trash2 size={14} />
                      </button>
                    </li>
                  )
                })}
            </ul>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                id={`exclusion-${type}`}
                className="form-input"
                placeholder={`Add ${type} exclusion`}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    const input = e.currentTarget
                    if (input.value.trim()) {
                      addExclusion(type as ExclusionType)
                      updateExclusion(form.exclusions.length, input.value.trim())
                      input.value = ''
                    }
                  }
                }}
                aria-label={`Add ${type} exclusion`}
              />
              <button
                type="button"
                className="button"
                onClick={(e) => {
                  const input = e.currentTarget.previousElementSibling as HTMLInputElement
                  if (input.value.trim()) {
                    addExclusion(type as ExclusionType)
                    updateExclusion(form.exclusions.length, input.value.trim())
                    input.value = ''
                  }
                }}
              >
                Add
              </button>
            </div>
          </div>
        ))}
        <div style={{ marginTop: 16, display: 'grid', gap: 12, gridTemplateColumns: 'repeat(2, 1fr)' }}>
          <div>
            <label className="form-label" htmlFor="duration-min">Min duration (seconds)</label>
            <input
              id="duration-min"
              type="number"
              min={0}
              className="form-input"
              value={form.exclusions.find((e) => e.exclusion_type === 'duration_min')?.exclusion_value ?? ''}
              onChange={(e) => {
                const value = e.target.value
                setForm((f) => {
                  const idx = f.exclusions.findIndex((ex) => ex.exclusion_type === 'duration_min')
                  if (idx >= 0) {
                    const exclusions = [...f.exclusions]
                    exclusions[idx] = { ...exclusions[idx], exclusion_value: value }
                    return { ...f, exclusions }
                  }
                  if (!value) return f
                  return { ...f, exclusions: [...f.exclusions, { exclusion_type: 'duration_min', exclusion_value: value }] }
                })
              }}
            />
          </div>
          <div>
            <label className="form-label" htmlFor="duration-max">Max duration (seconds)</label>
            <input
              id="duration-max"
              type="number"
              min={0}
              className="form-input"
              value={form.exclusions.find((e) => e.exclusion_type === 'duration_max')?.exclusion_value ?? ''}
              onChange={(e) => {
                const value = e.target.value
                setForm((f) => {
                  const idx = f.exclusions.findIndex((ex) => ex.exclusion_type === 'duration_max')
                  if (idx >= 0) {
                    const exclusions = [...f.exclusions]
                    exclusions[idx] = { ...exclusions[idx], exclusion_value: value }
                    return { ...f, exclusions }
                  }
                  if (!value) return f
                  return { ...f, exclusions: [...f.exclusions, { exclusion_type: 'duration_max', exclusion_value: value }] }
                })
              }}
            />
          </div>
        </div>
        {errors.duration && <div className="form-error">{errors.duration}</div>}
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Actions</h2>
        {form.actions.map((a, i) => (
          <div key={i}>
            <div className="field-row" style={{ gridTemplateColumns: '1fr auto', alignItems: 'end' }}>
              <select
                id={`action-${i}-playlist`}
                className="form-select"
                value={a.target_playlist_id}
                onChange={(e) => updateAction(i, { target_playlist_id: e.target.value })}
                disabled={playlistsLoading || playlistsError !== null}
                aria-label="Select target playlist"
                aria-busy={playlistsLoading}
                aria-invalid={!!errors[`action-${i}-playlist`]}
                aria-describedby={errors[`action-${i}-playlist`] ? `action-${i}-playlist-error` : undefined}
              >
                <option value="">{playlistsLoading ? 'Loading playlists...' : 'Select a playlist'}</option>
                {playlists.map((p) => (
                  <option key={p.playlist_id} value={p.playlist_id}>
                    {p.playlist_name}
                  </option>
                ))}
                {a.target_playlist_id && !playlists.some((p) => p.playlist_id === a.target_playlist_id) && (
                  <option value={a.target_playlist_id}>{a.target_playlist_id} (unavailable)</option>
                )}
              </select>
              {playlistsLoading && <Loader2 size={16} className="spinner" aria-hidden="true" />}
              <button type="button" className="icon-button" onClick={() => removeAction(i)} aria-label={`Remove action ${i + 1}`}>
                <Trash2 size={16} />
              </button>
            </div>
            {playlistsLoading && (
              <div aria-live="polite" style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                Loading playlists
              </div>
            )}
            {!playlistsLoading && playlists.length === 0 && playlistsError === null && (
              <div aria-live="polite" style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
                No playlists found.{ ' ' }
                <a
                  href="https://www.youtube.com"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  Create a playlist on YouTube first.
                </a>
              </div>
            )}
            {playlistsError && (
              <div className="form-error" role="alert" style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <AlertCircle size={14} aria-hidden="true" />
                {playlistsError}
                <button type="button" className="button" style={{ marginLeft: 8 }} onClick={loadPlaylists}>
                  Retry
                </button>
              </div>
            )}
            {errors[`action-${i}-playlist`] && (
              <div id={`action-${i}-playlist-error`} className="form-error" aria-live="polite">
                {errors[`action-${i}-playlist`]}
              </div>
            )}
          </div>
        ))}
        {errors.actions && (
          <div id="actions-error" className="form-error" aria-live="polite">
            {errors.actions}
          </div>
        )}
        <button id="add-action" type="button" className="button" onClick={addAction}>
          <Plus size={16} /> Add playlist action
        </button>
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Settings</h2>
        <label className="form-label" htmlFor="route-order">
          Evaluation order
        </label>
        <input
          id="route-order"
          type="number"
          min={1}
          className="form-input"
          style={{ maxWidth: 160 }}
          value={form.evaluation_order || 1}
          onChange={(e) => setForm((f) => ({ ...f, evaluation_order: Number(e.target.value) }))}
        />

        <div style={{ marginTop: 16, display: 'flex', gap: 24, flexWrap: 'wrap' }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
            <input
              type="checkbox"
              checked={form.is_final}
              onChange={(e) => setForm((f) => ({ ...f, is_final: e.target.checked }))}
            />
            Final route (stop evaluation after match)
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
            <span className="switch">
              <span className="switch-track" aria-hidden="true" />
              <input
                type="checkbox"
                role="switch"
                aria-checked={form.is_enabled}
                aria-label={form.is_enabled ? `Disable route ${form.name || 'this route'}` : `Enable route ${form.name || 'this route'}`}
                checked={form.is_enabled}
                onChange={(e) => setForm((f) => ({ ...f, is_enabled: e.target.checked }))}
              />
              <span className="switch-thumb" aria-hidden="true">
                {form.is_enabled ? <Check size={12} /> : <X size={12} />}
              </span>
            </span>
            <span style={{ color: form.is_enabled ? 'var(--color-status-accepted)' : 'var(--color-status-rejected)', fontWeight: 500 }}>
              {form.is_enabled ? 'Enabled' : 'Disabled'}
            </span>
          </label>
        </div>
      </div>

      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
        <button type="submit" className="button button--primary">
          Save route
        </button>
        <button type="button" className="button" onClick={handleCancel}>
          Cancel
        </button>
        {onDelete && (
          <button
            type="button"
            className="button button--danger"
            onClick={() => setShowDeleteDialog(true)}
          >
            Delete
          </button>
        )}
      </div>
      {showDeleteDialog && onDelete && (
        <DeleteConfirmDialog
          routeName={route?.name ?? form.name}
          onCancel={() => setShowDeleteDialog(false)}
          onConfirm={() => {
            setShowDeleteDialog(false)
            onDelete()
          }}
        />
      )}
    </form>
  )
}

export function RouteReorderHandle({ name, index, total, onMove }: { name: string; index: number; total: number; onMove: (from: number, to: number) => void }) {
  return (
    <div style={{ display: 'flex', gap: 4, alignItems: 'center' }}>
      <span aria-hidden="true" style={{ color: 'var(--color-text-muted)' }}>
        <GripVertical size={16} />
      </span>
      <button
        type="button"
        className="icon-button"
        disabled={index === 0}
        onClick={() => onMove(index, index - 1)}
        aria-label={`Move ${name} up`}
      >
        <ArrowUp size={16} />
      </button>
      <button
        type="button"
        className="icon-button"
        disabled={index === total - 1}
        onClick={() => onMove(index, index + 1)}
        aria-label={`Move ${name} down`}
      >
        <ArrowDown size={16} />
      </button>
    </div>
  )
}
