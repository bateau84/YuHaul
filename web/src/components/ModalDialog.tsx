import { useEffect, useRef, type ReactNode, type RefObject } from 'react'

const FOCUSABLE_SELECTOR = 'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'

interface ModalDialogProps {
  /** id of the element that labels the dialog */
  labelledBy: string
  /** id of the element that describes the dialog */
  describedBy?: string
  role?: 'dialog' | 'alertdialog'
  /** Element focused when the dialog opens; defaults to the first focusable element */
  initialFocusRef?: RefObject<HTMLElement | null>
  onCancel: () => void
  children: ReactNode
}

/**
 * Accessible modal dialog shell: focus moves into the dialog on open, Tab and
 * Shift+Tab are trapped inside it, Escape and backdrop clicks cancel, and focus
 * returns to the element that had it before the dialog opened.
 */
export function ModalDialog({
  labelledBy,
  describedBy,
  role = 'dialog',
  initialFocusRef,
  onCancel,
  children,
}: ModalDialogProps) {
  const dialogRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const previouslyFocused =
      document.activeElement instanceof HTMLElement ? document.activeElement : null
    const target = initialFocusRef?.current ?? dialogRef.current?.querySelector<HTMLElement>(FOCUSABLE_SELECTOR)
    target?.focus()
    return () => previouslyFocused?.focus()
  }, [initialFocusRef])

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [onCancel])

  function trapTab(e: React.KeyboardEvent<HTMLDivElement>) {
    if (e.key !== 'Tab') return
    const focusables = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE_SELECTOR) ?? [])
    if (focusables.length === 0) return
    const first = focusables[0]
    const last = focusables[focusables.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  return (
    <div
      className="modal-backdrop"
      role="presentation"
      onClick={(e) => {
        if (e.target === e.currentTarget) onCancel()
      }}
    >
      <div
        ref={dialogRef}
        className="modal"
        role={role}
        aria-modal="true"
        aria-labelledby={labelledBy}
        aria-describedby={describedBy}
        onKeyDown={trapTab}
      >
        {children}
      </div>
    </div>
  )
}
