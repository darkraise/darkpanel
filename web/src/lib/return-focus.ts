import { useEffect, useRef, type RefObject } from "react"

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

// FALLBACK_FOCUS marks a page's primary button. A dialog that a row menu
// opened returns focus to that menu's button; when the change removed the
// row, the button is gone and focus would fall to the page body.
export const FALLBACK_FOCUS = "data-focus-fallback"

// useFocusFallback moves focus to the page's FALLBACK_FOCUS button when a
// dialog closes and leaves focus on the body.
export function useFocusFallback(open: boolean) {
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open) {
      wasOpen.current = true
      return
    }
    if (!wasOpen.current) return
    wasOpen.current = false
    const id = window.setTimeout(() => {
      if (document.activeElement === null || document.activeElement === document.body) {
        document.querySelector<HTMLElement>(`[${FALLBACK_FOCUS}]`)?.focus()
      }
    })
    return () => window.clearTimeout(id)
  }, [open])
}
