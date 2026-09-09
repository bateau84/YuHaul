import { useRef, useEffect, type ReactNode } from 'react'

interface ConfirmDialogProps {
  open: boolean
  title: string
  children: ReactNode
  confirmLabel?: string
  cancelLabel?: string
  destructive?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  open,
  title,
  children,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  destructive,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const ref = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = ref.current
    if (!dialog) return
    if (open && !dialog.open) {
      dialog.showModal()
    } else if (!open && dialog.open) {
      dialog.close()
    }
  }, [open])

  return (
    <dialog ref={ref} onCancel={onCancel} className="card">
      <h2 style={{ marginTop: 0 }}>{title}</h2>
      <div style={{ marginBottom: 24 }}>{children}</div>
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 12 }}>
        <button className="button" onClick={onCancel}>
          {cancelLabel}
        </button>
        <button
          className={`button ${destructive ? 'button--danger' : 'button--primary'}`}
          onClick={onConfirm}
        >
          {confirmLabel}
        </button>
      </div>
    </dialog>
  )
}
