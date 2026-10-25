import { forwardRef } from 'react'
import { AlertTriangle, Loader2 } from 'lucide-react'

interface FetchErrorRegionProps {
  message: string
  onRetry: () => void
  retrying: boolean
}

/**
 * Inline fetch-error region shared by list views, per
 * docs/design/amendment-list-fetch-error-handling.md: announces the failure
 * with role="alert"/aria-live="assertive" and offers Retry as the recovery
 * action. While `retrying` the button shows "Retrying…" and stays disabled;
 * a failed retry re-enables it (the view stays in the fetch-error state).
 */
export const FetchErrorRegion = forwardRef<HTMLButtonElement, FetchErrorRegionProps>(
  function FetchErrorRegion({ message, onRetry, retrying }, ref) {
    return (
      <div
        className="card"
        role="alert"
        aria-live="assertive"
        style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start', gap: 12 }}
      >
        <p style={{ margin: 0, display: 'flex', alignItems: 'center', gap: 8, color: 'var(--color-status-failed)' }}>
          <AlertTriangle size={16} aria-hidden="true" />
          {message}
        </p>
        <button type="button" className="button" onClick={onRetry} disabled={retrying} ref={ref}>
          {retrying && <Loader2 size={16} className="spinner" aria-hidden="true" />}
          {retrying ? 'Retrying…' : 'Retry'}
        </button>
      </div>
    )
  }
)
