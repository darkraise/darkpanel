import { useCallback, useEffect, useRef, type RefObject } from "react"

// darkraise-ui's focus trap returns focus to whatever was focused when its
// effect ran. A field that autofocuses on mount is focused by then and gone
// after the dialog closes, so the button that opened the flow takes focus back.
// When an apply removed that button, the fallback takes it instead.
export function useReturnFocus(open: boolean, fallback?: RefObject<HTMLElement | null>) {
  const button = useRef<HTMLButtonElement>(null)
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open) {
      wasOpen.current = true
      return
    }
    if (!wasOpen.current) return
    wasOpen.current = false
    if (button.current?.isConnected === true) button.current.focus()
    else fallback?.current?.focus()
  }, [open, fallback])
  return button
}

// FALLBACK_FOCUS marks a page's primary button, which takes focus when a
// dialog closes after its change removed the row that opened it.
export const FALLBACK_FOCUS = "data-focus-fallback"

// The row-menu button whose item opened the dialog now open, if any.
let dialogTrigger: HTMLElement | null = null

// useMenuDialogFocus is for a row menu whose items open dialogs. The menu
// returns focus to its button when its content unmounts, after its exit
// animation, which on a busy device lands after the dialog has taken focus
// and pulls focus out of the open dialog. pick marks the item's choice so
// the menu leaves focus alone; the page's useFocusFallback brings focus back
// to the button when the dialog closes.
export function useMenuDialogFocus<T>(onPick: (d: T) => void) {
  const trigger = useRef<HTMLButtonElement>(null)
  const picked = useRef(false)
  const pick = useCallback(
    (d: T) => {
      picked.current = true
      dialogTrigger = trigger.current
      onPick(d)
    },
    [onPick],
  )
  const onCloseAutoFocus = useCallback((event: Event) => {
    if (!picked.current) return
    picked.current = false
    event.preventDefault()
  }, [])
  return { trigger, pick, onCloseAutoFocus }
}

// useFocusFallback returns focus when a page's dialog closes and leaves it
// on the body: to the row-menu button that opened the dialog, or, when the
// change removed that row, to the page's FALLBACK_FOCUS button. A closing
// dialog keeps focus in its leftover nodes until its exit animation ends,
// and drops it on the body after, so it watches for up to five seconds and
// acts only on the body.
export function useFocusFallback(open: boolean) {
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open) {
      wasOpen.current = true
      return
    }
    if (!wasOpen.current) return
    wasOpen.current = false
    let tries = 0
    const id = window.setInterval(() => {
      const active = document.activeElement
      const target = dialogTrigger?.isConnected === true ? dialogTrigger : document.querySelector<HTMLElement>(`[${FALLBACK_FOCUS}]`)
      if (active === null || active === document.body) target?.focus()
      else if (active !== target && tries++ < 100) return
      window.clearInterval(id)
      dialogTrigger = null
    }, 50)
    return () => window.clearInterval(id)
  }, [open])
}

// A form kept mounted, hidden, while its change is reviewed reopens with focus
// on its submit button, the control that started the review, rather than on
// the first field. The kit's focus trap picks that field in the dialog's own
// effect; this effect runs after it, since a parent's effects follow its
// children's.
export function useReopenFocus(hidden: boolean) {
  const submit = useRef<HTMLButtonElement>(null)
  const wasHidden = useRef(false)
  useEffect(() => {
    if (hidden) {
      wasHidden.current = true
      return
    }
    if (!wasHidden.current) return
    wasHidden.current = false
    submit.current?.focus()
  }, [hidden])
  return submit
}
