// Browsers expose navigator.clipboard only in a secure context, and the panel
// can be served over plain HTTP on a public address, where Copy would
// otherwise always fail. The fallback selects the field that shows the text,
// or a temporary textarea when none does, and copies with execCommand.
export function installClipboardFallback(): void {
  if (typeof navigator.clipboard?.writeText === "function") return
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: {
      async writeText(text: string): Promise<void> {
        const before = document.activeElement
        // A field behind an open dialog may hold the same text; the Copy
        // button that called this sits in the dialog to copy from.
        const scope: ParentNode = before?.closest('[role="dialog"], [role="alertdialog"]') ?? document
        const shown = Array.from(scope.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>("input, textarea")).find(
          (el) => el.value === text,
        )
        let area: HTMLTextAreaElement | undefined
        let field: HTMLInputElement | HTMLTextAreaElement
        if (shown !== undefined) {
          field = shown
        } else {
          area = document.createElement("textarea")
          area.value = text
          area.readOnly = true
          area.style.position = "fixed"
          area.style.left = "-9999px"
          area.style.top = "0"
          document.body.appendChild(area)
          field = area
        }
        let copied: boolean
        try {
          field.focus()
          field.select()
          copied = document.execCommand("copy")
        } finally {
          area?.remove()
          if (before instanceof HTMLElement) before.focus()
        }
        if (!copied) throw new Error("copy is not available")
      },
    },
  })
}
