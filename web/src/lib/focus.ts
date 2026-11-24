import { useRef } from 'react'

// Ref callbacks that focus an element in the commit that attaches it. A passive
// useEffect runs after paint, so focus would lag behind the DOM and a keyboard
// user (or a test) could act in the gap. These are module-level so their identity
// is stable: a re-render never detaches and re-attaches the ref, so never refocuses.
export function focusOnAttach(el: HTMLElement | null) {
  el?.focus()
}

export function focusOnAttachWithoutScroll(el: HTMLElement | null) {
  el?.focus({ preventScroll: true })
}

// One-shot focus for the View video link of a row that has just been closed or
// removed under a person's Retry: the pressed Retry control is gone, so focus
// goes to the row's link rather than falling to <body>. arm(id) is called with
// the response; refFor(id) is the link's ref, and takes the focus once when the
// armed row's link attaches. A row that arrives closed from a read, or that
// re-mounts later (a tab switch), is never armed and so never takes focus.
export function useLinkFocusOnce() {
  const armed = useRef<number | null>(null)
  return {
    arm(id: number) {
      armed.current = id
    },
    refFor(id: number) {
      return (el: HTMLElement | null) => {
        if (el && armed.current === id) {
          armed.current = null
          el.focus()
        }
      }
    },
  }
}
