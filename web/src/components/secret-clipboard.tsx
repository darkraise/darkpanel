import { useState, type ReactNode } from "react"
import { Clipboard } from "darkraise-ui/components/clipboard"

// darkraise-ui reports a failed copy only through a toast, and the app mounts
// no toaster; on a plain-HTTP origin the copy can fail, and the dialogs that
// use this show their secret only once.
export function SecretClipboard({ value, children }: { value: string; children: ReactNode }) {
  const [failed, setFailed] = useState(false)
  return (
    <Clipboard
      value={value}
      onCopyStatusChange={(d) => {
        if (d.copied) setFailed(false)
        else if (d.error !== undefined) setFailed(true)
      }}
    >
      {children}
      {failed && (
        <p role="alert" className="text-sm text-destructive">
          Copy failed. Select the text and copy it yourself.
        </p>
      )}
    </Clipboard>
  )
}
