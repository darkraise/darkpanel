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
