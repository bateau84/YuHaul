import { useRef } from 'react'
import { ModalDialog } from './ModalDialog'

interface DeleteConfirmDialogProps {
  /** Disambiguated route label, e.g. `Music Discovery (a3f5b2c1)` — same-named
   * routes must be distinguishable in the confirmation (design amendment). */
  routeLabel: string
  onCancel: () => void
  onConfirm: () => void
}

export function DeleteConfirmDialog({ routeLabel, onCancel, onConfirm }: DeleteConfirmDialogProps) {
  const cancelRef = useRef<HTMLButtonElement>(null)

  return (
    <ModalDialog
      role="alertdialog"
      labelledBy="delete-route-title"
      describedBy="delete-route-body"
      initialFocusRef={cancelRef}
      onCancel={onCancel}
    >
      <h2 id="delete-route-title" className="modal-title">
        Delete route?
      </h2>
      <p id="delete-route-body">
        Delete route '{routeLabel}'? This cannot be undone. Videos that would have matched this
        route will no longer be routed by it.
      </p>
      <div className="modal-actions">
        <button type="button" className="button" ref={cancelRef} onClick={onCancel}>
          Cancel
        </button>
        <button type="button" className="button button--danger" onClick={onConfirm}>
          Delete
        </button>
      </div>
    </ModalDialog>
  )
}
