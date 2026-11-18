// Browsers expose navigator.clipboard only in a secure context, and the panel
// can be served over plain HTTP on a public address, where Copy would
// otherwise always fail. The fallback copies through a selected textarea.
export function installClipboardFallback(): void {
  if (typeof navigator.clipboard?.writeText === "function") return
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: {
      async writeText(text: string): Promise<void> {
        const area = document.createElement("textarea")
        area.value = text
        area.readOnly = true
        area.style.position = "fixed"
        area.style.left = "-9999px"
        area.style.top = "0"
        document.body.appendChild(area)
        let copied: boolean
        try {
          area.focus()
          area.select()
          copied = document.execCommand("copy")
        } finally {
          area.remove()
        }
        if (!copied) throw new Error("copy is not available")
      },
    },
  })
}
