import { useState, useEffect } from 'react'
import { Trash2, Plus, GripVertical, ArrowUp, ArrowDown } from 'lucide-react'
import type { Route, RouteCondition, RouteAction, Composition, ConditionField, ConditionOperator, ExclusionType } from '../types'

const fields: { value: ConditionField; label: string }[] = [
  { value: 'title', label: 'Title' },
  { value: 'channel_id', label: 'Channel ID' },
  { value: 'channel_name', label: 'Channel name' },
  { value: 'description', label: 'Description' },
  { value: 'duration_seconds', label: 'Duration (seconds)' },
]

const operators: { value: ConditionOperator; label: string }[] = [
  { value: 'contains', label: 'contains' },
  { value: 'not_contains', label: 'does not contain' },
  { value: 'equals', label: 'equals' },
  { value: 'not_equals', label: 'does not equal' },
  { value: 'gt', label: 'greater than' },
  { value: 'gte', label: 'greater than or equal' },
  { value: 'lt', label: 'less than' },
  { value: 'lte', label: 'less than or equal' },
  { value: 'in_list', label: 'is in list' },
]

const blankRoute = (): Route => ({
  id: 0,
  name: '',
  description: '',
  evaluation_order: 0,
  is_final: false,
  is_enabled: true,
  condition_composition: 'AND',
  conditions: [],
  actions: [],
  exclusions: [],
  created_at: '',
  updated_at: '',
})

interface RouteEditorProps {
  route?: Route
  onSave: (route: Route) => void
  onCancel: () => void
  onDelete?: () => void
}

export function RouteEditor({ route, onSave, onCancel, onDelete }: RouteEditorProps) {
  const [form, setForm] = useState<Route>(blankRoute())
  const [errors, setErrors] = useState<Record<string, string>>({})

  useEffect(() => {
    setForm(route ? { ...route } : blankRoute())
    setErrors({})
  }, [route])

  function validate(): boolean {
    const next: Record<string, string> = {}
    if (!form.name.trim()) next.name = 'Route name is required'
    if (form.conditions.some((c) => !c.value.trim())) next.conditions = 'Condition values cannot be empty'
    if (form.actions.some((a) => !a.target_playlist_id.trim())) next.actions = 'Playlist targets cannot be empty'
    const minDuration = form.exclusions.find((e) => e.exclusion_type === 'duration_min')?.exclusion_value
    const maxDuration = form.exclusions.find((e) => e.exclusion_type === 'duration_max')?.exclusion_value
    if (minDuration && maxDuration && Number(minDuration) > Number(maxDuration)) {
      next.duration = 'Minimum duration must be less than or equal to maximum'
    }
    setErrors(next)
    return Object.keys(next).length === 0
  }

  function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!validate()) return
    onSave(form)
  }

  function updateCondition(index: number, patch: Partial<RouteCondition>) {
    setForm((f) => {
      const conditions = [...f.conditions]
      conditions[index] = { ...conditions[index], ...patch }
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
          aria-describedby={errors.name ? 'route-name-error' : undefined}
          autoFocus
        />
        {errors.name && <div id="route-name-error" className="form-error">{errors.name}</div>}

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
          <div key={i} className="field-row" style={{ alignItems: 'end' }}>
            <select
              className="form-select"
              value={c.field}
              onChange={(e) => updateCondition(i, { field: e.target.value as ConditionField })}
              aria-label={`Condition ${i + 1} field`}
            >
              {fields.map((f) => (
                <option key={f.value} value={f.value}>
                  {f.label}
                </option>
              ))}
            </select>
            <select
              className="form-select"
              value={c.operator}
              onChange={(e) => updateCondition(i, { operator: e.target.value as ConditionOperator })}
              aria-label={`Condition ${i + 1} operator`}
            >
              {operators.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            <input
              className="form-input"
              value={c.value}
              onChange={(e) => updateCondition(i, { value: e.target.value })}
              placeholder="Value"
              aria-label={`Condition ${i + 1} value`}
            />
            <button type="button" className="icon-button" onClick={() => removeCondition(i)} aria-label={`Remove condition ${i + 1}`}>
              <Trash2 size={16} />
            </button>
          </div>
        ))}
        {errors.conditions && <div className="form-error">{errors.conditions}</div>}
        <button type="button" className="button" onClick={addCondition}>
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
                        style={{ width: 20, height: 20, border: 'none' }}
                        onClick={() => removeExclusion(globalIdx)}
                        aria-label={`Remove ${e.exclusion_value}`}
                      >
                        <Trash2 size={12} />
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
                const idx = form.exclusions.findIndex((e) => e.exclusion_type === 'duration_min')
                if (idx >= 0) updateExclusion(idx, e.target.value)
                else if (e.target.value) addExclusion('duration_min')
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
                const idx = form.exclusions.findIndex((e) => e.exclusion_type === 'duration_max')
                if (idx >= 0) updateExclusion(idx, e.target.value)
                else if (e.target.value) addExclusion('duration_max')
              }}
            />
          </div>
        </div>
        {errors.duration && <div className="form-error">{errors.duration}</div>}
      </div>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">Actions</h2>
        {form.actions.map((a, i) => (
          <div key={i} className="field-row" style={{ gridTemplateColumns: '1fr auto', alignItems: 'end' }}>
            <input
              className="form-input"
              value={a.target_playlist_id}
              onChange={(e) => updateAction(i, { target_playlist_id: e.target.value })}
              placeholder="Target playlist ID"
              aria-label={`Action ${i + 1} target playlist`}
            />
            <button type="button" className="icon-button" onClick={() => removeAction(i)} aria-label={`Remove action ${i + 1}`}>
              <Trash2 size={16} />
            </button>
          </div>
        ))}
        {errors.actions && <div className="form-error">{errors.actions}</div>}
        <button type="button" className="button" onClick={addAction}>
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
            <input
              type="checkbox"
              checked={form.is_enabled}
              onChange={(e) => setForm((f) => ({ ...f, is_enabled: e.target.checked }))}
            />
            Enabled
          </label>
        </div>
      </div>

      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
        <button type="submit" className="button button--primary">
          Save route
        </button>
        <button type="button" className="button" onClick={onCancel}>
          Cancel
        </button>
        {onDelete && (
          <button type="button" className="button button--danger" onClick={onDelete}>
            Delete
          </button>
        )}
      </div>
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
