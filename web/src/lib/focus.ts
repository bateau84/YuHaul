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
