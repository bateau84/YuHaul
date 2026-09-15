import { useEffect, useRef } from 'react'

interface DeleteConfirmDialogProps {
  routeName: string
  onCancel: () => void
  onConfirm: () => void
}

export function DeleteConfirmDialog({ routeName, onCancel, onConfirm }: DeleteConfirmDialogProps) {
  const cancelRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    cancelRef.current?.focus()
  }, [])

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        onCancel()
      }
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onCancel])

  return (
    <div
      className="modal-backdrop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel()
      }}
      role="presentation"
    >
      <div
        className="modal"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="delete-route-title"
        aria-describedby="delete-route-body"
      >
        <h2 id="delete-route-title" className="modal-title">
          Delete route?
        </h2>
        <p id="delete-route-body">
          Delete route '{routeName}'? This cannot be undone. Videos that would have matched this
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
      </div>
    </div>
  )
}
