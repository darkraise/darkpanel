import { useEffect, useRef } from "react"

// darkraise-ui's focus trap returns focus to whatever was focused when its
// effect ran. A field that autofocuses on mount is focused by then and gone
// after the dialog closes, so the button that opened the flow takes focus back.
export function useReturnFocus(open: boolean) {
  const button = useRef<HTMLButtonElement>(null)
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open) {
      wasOpen.current = true
      return
    }
    if (!wasOpen.current) return
    wasOpen.current = false
    button.current?.focus()
  }, [open])
  return button
}
