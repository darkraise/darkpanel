import { useEffect, useRef, useState } from "react"
import { Download, Save, X } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Textarea } from "darkraise-ui/components/textarea"
import { ApiError, errorMessage } from "@/api/client"
import { CONFLICT, downloadFile, MAX_TEXT, NOT_TEXT, readText, writeText } from "./files"

type Load = { kind: "loading" } | { kind: "not text" } | { kind: "error"; message: string } | { kind: "ready" }

export interface FileEditorProps {
  hostId: string
  path: string
  // size is the listed size; a file over MAX_TEXT is never read.
  size: number
  onClose: () => void
}

// FileEditor edits a text file of at most 1 MiB (UX-9). Opening it reads the
// file, which asks for a code outside the step-up window; a save carries the
// hash the editor opened, and a file changed since shows Reload and
// Overwrite.
export function FileEditor({ hostId, path, size, onClose }: FileEditorProps) {
  const [load, setLoad] = useState<Load>(size > MAX_TEXT ? { kind: "not text" } : { kind: "loading" })
  const [text, setText] = useState("")
  const [saved, setSaved] = useState("")
  const sha = useRef("")
  const [saving, setSaving] = useState(false)
  const [conflict, setConflict] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [round, setRound] = useState(0)
  const area = useRef<HTMLTextAreaElement>(null)

  // Reads asking for a code at once share one step-up dialog, so React's
  // second run of this effect in development asks no second code.
  useEffect(() => {
    if (size > MAX_TEXT) return
    let live = true
    let focusTimer = 0
    readText(hostId, path).then(
      (f) => {
        if (!live) return
        sha.current = f.sha256
        setText(f.text)
        setSaved(f.text)
        setConflict(false)
        setLoad({ kind: "ready" })
        // The read may have waited on a code: a closing step-up dialog drops
        // focus on the body after its exit animation, so the editor takes
        // focus once no dialog is left and the body holds it.
        let tries = 0
        focusTimer = window.setInterval(() => {
          const dialog = document.querySelector("[role=dialog]") !== null
          const active = document.activeElement
          if (!dialog && (active === null || active === document.body)) area.current?.focus()
          if ((!dialog && document.activeElement === area.current) || tries++ > 100) window.clearInterval(focusTimer)
        }, 50)
      },
      (err: unknown) => {
        if (!live) return
        setLoad(err instanceof ApiError && err.code === "files.not_text" ? { kind: "not text" } : { kind: "error", message: errorMessage(err) })
      },
    )
    return () => {
      live = false
      window.clearInterval(focusTimer)
    }
  }, [hostId, path, size, round])

  function reload() {
    setLoad({ kind: "loading" })
    setRound((n) => n + 1)
  }

  async function save(expect: string) {
    setSaving(true)
    setSaveError(null)
    try {
      const written = await writeText(hostId, path, text, expect)
      sha.current = written.sha256
      setSaved(text)
      setConflict(false)
      toast.success(`Saved ${path}`)
    } catch (err) {
      if (err instanceof ApiError && err.code === "files.conflict") setConflict(true)
      else setSaveError(errorMessage(err))
    } finally {
      setSaving(false)
    }
  }

  async function overwrite() {
    setSaving(true)
    try {
      const current = await readText(hostId, path)
      await save(current.sha256)
    } catch (err) {
      setSaveError(errorMessage(err))
      setSaving(false)
    }
  }

  async function download() {
    try {
      await downloadFile(hostId, path)
    } catch (err) {
      toast.error(`Couldn't download ${path}`, { description: errorMessage(err) })
    }
  }

  const close = (
    <Button variant="outline" onClick={onClose}>
      <X aria-hidden="true" />
      Close
    </Button>
  )

  return (
    <section aria-label={`Editing ${path}`} className="flex flex-col gap-3">
      {load.kind === "loading" && (
        <div role="status" aria-label={`Loading ${path}`} className="flex flex-col gap-2">
          <Skeleton className="h-5 w-48" />
          <Skeleton className="h-64 w-full" />
        </div>
      )}
      {load.kind === "not text" && (
        <>
          <Alert role="status">
            <AlertTitle as="h3" className="break-all">
              {path}
            </AlertTitle>
            <AlertDescription>{NOT_TEXT}</AlertDescription>
          </Alert>
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => void download()}>
              <Download aria-hidden="true" />
              Download
            </Button>
            {close}
          </div>
        </>
      )}
      {load.kind === "error" && (
        <>
          <Alert variant="destructive" role="alert">
            <AlertTitle as="h3">{`Couldn't open ${path}`}</AlertTitle>
            <AlertDescription>{load.message}</AlertDescription>
          </Alert>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={reload}>
              Try again
            </Button>
            {close}
          </div>
        </>
      )}
      {load.kind === "ready" && (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault()
            void save(sha.current)
          }}
        >
          <Label htmlFor="file-editor-text" className="break-all font-mono">
            {path}
          </Label>
          <Textarea
            id="file-editor-text"
            ref={area}
            value={text}
            onChange={(e) => setText(e.target.value)}
            spellCheck={false}
            rows={20}
            className="font-mono text-xs"
          />
          {conflict && (
            <Alert variant="warning" role="alert">
              <AlertTitle as="h3">{CONFLICT}</AlertTitle>
              <AlertDescription className="flex flex-wrap gap-2">
                <Button type="button" size="sm" variant="outline" onClick={reload}>
                  Reload
                </Button>
                <Button type="button" size="sm" variant="destructive" disabled={saving} onClick={() => void overwrite()}>
                  Overwrite
                </Button>
              </AlertDescription>
            </Alert>
          )}
          {saveError !== null && (
            <Alert variant="destructive" role="alert">
              <AlertTitle as="h3">{`Couldn't save ${path}`}</AlertTitle>
              <AlertDescription>{saveError}</AlertDescription>
            </Alert>
          )}
          <div className="flex flex-wrap items-center gap-2">
            <Button type="submit" disabled={saving}>
              <Save aria-hidden="true" />
              Save
            </Button>
            {close}
            {text !== saved && <span className="text-xs text-muted-foreground">Unsaved changes</span>}
          </div>
        </form>
      )}
    </section>
  )
}
