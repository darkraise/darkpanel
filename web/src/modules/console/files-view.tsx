import { useRef, useState, type FormEvent } from "react"
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query"
import { useSearchParams } from "react-router"
import { ChevronRight, FolderPlus, MoreHorizontal, Upload } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Progress } from "darkraise-ui/components/progress"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage } from "@/api/client"
import { FALLBACK_FOCUS, useFocusFallback, useMenuDialogFocus } from "@/lib/return-focus"
import { FileEditor } from "./file-editor"
import {
  baseName,
  changeFile,
  childPath,
  crumbs,
  downloadFile,
  ESCAPED_REASON,
  formatSize,
  listFiles,
  MODE_PATTERN,
  normalPath,
  parentPath,
  uploadFile,
  type FileChange,
  type FileInfo,
} from "./files"

type Op = "rename" | "mode" | "delete" | "mkdir"
type Pick = "download" | "edit" | Op

interface Pending {
  op: Op
  entry?: FileInfo
}

interface Uploading {
  name: string
  dir: string
  size: number
  sent: number
  abort: AbortController
}

const filesKey = (hostId: string, path: string) => ["files", hostId, path] as const

function shownName(e: FileInfo): string {
  if (e.type === "dir") return `${e.name}/`
  if (e.type === "symlink") return `${e.name} → ${e.target ?? ""}`
  return e.name
}

function modified(iso: string): string {
  return iso.slice(0, 16).replace("T", " ")
}

// MenuItem is one row-menu item, disabled with its reason on a line inside
// it, which keyboard users read where a hover tooltip would not show.
function MenuItem({ label, reason, onPick }: { label: string; reason?: string; onPick: () => void }) {
  return (
    <DropdownMenuItem disabled={reason !== undefined} onSelect={onPick}>
      <span className="flex max-w-64 flex-col">
        {label}
        {reason !== undefined && <span className="text-xs text-muted-foreground">{reason}</span>}
      </span>
    </DropdownMenuItem>
  )
}

function EntryMenu({ entry, onPick }: { entry: FileInfo; onPick: (e: FileInfo, p: Pick) => void }) {
  const { trigger, pick, onCloseAutoFocus } = useMenuDialogFocus((p: Pick) => onPick(entry, p))
  const reason = entry.escaped === true ? ESCAPED_REASON : entry.protected
  const file = entry.type === "file"
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button ref={trigger} variant="ghost" size="icon" aria-label={`Actions for ${entry.name}`}>
          <MoreHorizontal aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onCloseAutoFocus={onCloseAutoFocus}>
        {file && <MenuItem label="Download" reason={entry.escaped === true ? ESCAPED_REASON : undefined} onPick={() => pick("download")} />}
        {file && <MenuItem label="Edit" reason={reason} onPick={() => pick("edit")} />}
        <MenuItem label="Rename" reason={reason} onPick={() => pick("rename")} />
        <MenuItem label="Permissions" reason={reason} onPick={() => pick("mode")} />
        <MenuItem label="Delete" reason={reason} onPick={() => pick("delete")} />
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function PathBar({ path, onGo }: { path: string; onGo: (path: string) => void }) {
  const [typed, setTyped] = useState(path)
  const [shown, setShown] = useState(path)
  if (shown !== path) {
    setShown(path)
    setTyped(path)
  }
  const parts = crumbs(path)
  function submit(e: FormEvent) {
    e.preventDefault()
    if (typed.trim() !== "") onGo(normalPath(typed.trim()))
  }
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <nav aria-label="Breadcrumb" className="min-w-0">
        <ol className="flex flex-wrap items-center gap-1 text-sm">
          {parts.map((c, i) => (
            <li key={c.path} className="flex min-w-0 items-center gap-1">
              {i > 1 && <ChevronRight className="size-3 shrink-0" aria-hidden="true" />}
              {i === parts.length - 1 ? (
                <span aria-current="page" className="break-all font-mono">
                  {c.name}
                </span>
              ) : (
                <Button variant="link" size="sm" className="h-auto break-all p-0 font-mono" onClick={() => onGo(c.path)}>
                  {c.name}
                </Button>
              )}
            </li>
          ))}
        </ol>
      </nav>
      <form onSubmit={submit} className="flex max-w-xl items-end gap-2">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <Label htmlFor="files-path">Path</Label>
          <Input id="files-path" value={typed} onChange={(e) => setTyped(e.target.value)} className="font-mono" spellCheck={false} />
        </div>
        <Button type="submit" variant="outline">
          Go
        </Button>
      </form>
    </div>
  )
}

// OpDialog asks what a file change needs: a name, a mode, a typed path or
// only a confirmation.
function OpDialog({ hostId, dir, pending, onClose, onDone }: { hostId: string; dir: string; pending: Pending; onClose: () => void; onDone: () => void }) {
  const entry = pending.entry
  const target = entry === undefined ? dir : childPath(dir, entry.name)
  const dirDelete = pending.op === "delete" && entry?.type === "dir"
  const initial = pending.op === "rename" ? (entry?.name ?? "") : pending.op === "mode" ? (entry?.mode ?? "") : ""
  const [value, setValue] = useState(initial)
  const [submitted, setSubmitted] = useState(false)
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState<string | null>(null)

  const spec: { title: string; description: string; label?: string; button: string; error: string | null; run: () => [Parameters<typeof changeFile>[1], FileChange]; done: string } =
    pending.op === "mkdir"
      ? {
          title: "New folder",
          description: `In ${dir}`,
          label: "Folder name",
          button: "Create",
          error: nameError(value),
          run: () => ["mkdir", { path: childPath(dir, value) }],
          done: `Created ${childPath(dir, value)}`,
        }
      : pending.op === "rename"
        ? {
            title: `Rename ${entry?.name ?? ""}`,
            description: `In ${dir}`,
            label: "New name",
            button: "Rename",
            error: nameError(value),
            run: () => ["rename", { path: target, to: childPath(dir, value) }],
            done: `Renamed to ${value}`,
          }
        : pending.op === "mode"
          ? {
              title: `Permissions of ${entry?.name ?? ""}`,
              description: "The mode in octal, such as 0644 or 0755",
              label: "Mode",
              button: "Change",
              error: MODE_PATTERN.test(value) ? null : "Enter 3 or 4 octal digits",
              run: () => ["chmod", { path: target, mode: value }],
              done: `Changed the mode of ${entry?.name ?? ""} to ${value}`,
            }
          : dirDelete
            ? {
                title: `Delete ${entry?.name ?? ""} and everything in it`,
                description: `Type its path, ${target}, to delete it`,
                label: "Path",
                button: "Delete",
                error: value === target ? null : "Type the path exactly",
                run: () => ["delete", { path: target, confirm: value }],
                done: `Deleted ${target}`,
              }
            : {
                title: `Delete ${entry?.name ?? ""}`,
                description: `${target} is deleted from the host; this cannot be undone`,
                button: "Delete",
                error: null,
                run: () => ["delete", { path: target }],
                done: `Deleted ${target}`,
              }

  async function submit(e: FormEvent) {
    e.preventDefault()
    setSubmitted(true)
    if (spec.error !== null) return
    setBusy(true)
    setFailed(null)
    const [op, change] = spec.run()
    try {
      await changeFile(hostId, op, change)
      toast.success(spec.done)
      onDone()
    } catch (err) {
      setFailed(errorMessage(err))
      setBusy(false)
    }
  }

  const shownError = spec.label !== undefined && (submitted || value !== initial) ? spec.error : null
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <form onSubmit={(e) => void submit(e)} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle className="break-all">{spec.title}</DialogTitle>
            <DialogDescription className="break-all">{spec.description}</DialogDescription>
          </DialogHeader>
          {spec.label !== undefined && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="file-op-value">{spec.label}</Label>
              <Input
                id="file-op-value"
                autoFocus
                value={value}
                onChange={(e) => setValue(e.target.value)}
                className="font-mono"
                spellCheck={false}
                aria-invalid={shownError !== null}
                aria-describedby={shownError !== null ? "file-op-error" : undefined}
              />
              {shownError !== null && (
                <p id="file-op-error" className="text-sm text-destructive-text">
                  {shownError}
                </p>
              )}
            </div>
          )}
          {failed !== null && (
            <Alert variant="destructive" role="alert">
              <AlertTitle as="h3">Not done</AlertTitle>
              <AlertDescription>{failed}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" variant={pending.op === "delete" ? "destructive" : "default"} disabled={busy}>
              {spec.button}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function nameError(name: string): string | null {
  if (name === "" || name === "." || name === "..") return "Enter a name"
  if (name.includes("/")) return "A name cannot hold /"
  if (new TextEncoder().encode(name).length > 255) return "A name is at most 255 bytes"
  return null
}

function UploadProgress({ up }: { up: Uploading }) {
  const percent = up.size === 0 ? 100 : Math.floor((up.sent / up.size) * 100)
  return (
    <div className="flex max-w-lg flex-col gap-2">
      <span className="break-all text-sm">{`Uploading ${up.name} to ${up.dir}`}</span>
      <Progress value={percent} aria-label="Upload progress" getValueLabel={(v) => `${v}%`} />
      <div className="flex items-center gap-3">
        <span className="text-xs text-muted-foreground">{`${formatSize(up.sent)} of ${formatSize(up.size)} · ${percent}%`}</span>
        <Button size="sm" variant="outline" onClick={() => up.abort.abort()}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

// FilesView is the console's file manager (UI-5, UX-9): the directory in
// ?path=, so back and deep links work, a page of at most 2000 entries with
// Show more, and row menus whose writes are disabled on protected paths.
export function FilesView({ hostId }: { hostId: string }) {
  const [params, setParams] = useSearchParams()
  const path = normalPath(params.get("path") ?? "/")
  const client = useQueryClient()
  const [editing, setEditing] = useState<{ path: string; size: number } | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const [uploading, setUploading] = useState<Uploading | null>(null)
  const picker = useRef<HTMLInputElement>(null)
  const [shownPath, setShownPath] = useState(path)
  const [dirty, setDirty] = useState(false)
  // leaving: ?path= left the edited file's folder under unsaved text; the
  // editor stays until its user discards the text or goes back.
  const [leaving, setLeaving] = useState(false)
  if (shownPath !== path) {
    setShownPath(path)
    if (editing === null || !dirty) setEditing(null)
    else setLeaving(path !== parentPath(editing.path))
  }
  useFocusFallback(pending !== null || editing !== null)

  const list = useInfiniteQuery({
    queryKey: filesKey(hostId, path),
    queryFn: ({ pageParam }) => listFiles(hostId, path, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => (last.next === undefined || last.next === "" ? undefined : last.next),
  })

  function go(to: string) {
    setParams((p) => {
      const next = new URLSearchParams(p)
      next.set("path", to)
      return next
    })
  }

  const refresh = () => void client.invalidateQueries({ queryKey: filesKey(hostId, path) })

  async function download(target: string) {
    try {
      await downloadFile(hostId, target)
    } catch (err) {
      toast.error(`Couldn't download ${baseName(target)}`, { description: errorMessage(err) })
    }
  }

  function onPick(entry: FileInfo, p: Pick) {
    const target = childPath(path, entry.name)
    if (p === "download") void download(target)
    else if (p === "edit") setEditing({ path: target, size: entry.size })
    else setPending({ op: p, entry })
  }

  async function startUpload(file: File) {
    const up: Uploading = { name: file.name, dir: path, size: file.size, sent: 0, abort: new AbortController() }
    setUploading(up)
    try {
      await uploadFile(hostId, path, file, (sent) => setUploading((u) => (u === null ? u : { ...u, sent })), up.abort.signal)
      toast.success(`Uploaded ${file.name}`)
      refresh()
    } catch (err) {
      if (up.abort.signal.aborted) toast(`Upload of ${file.name} cancelled`)
      else toast.error(`Couldn't upload ${file.name}`, { description: errorMessage(err) })
    } finally {
      setUploading(null)
    }
  }

  const pages = list.data?.pages ?? []
  const entries = pages.flatMap((p) => p.entries)
  const protectedDir = pages[0]?.protected

  if (editing !== null) {
    const home = parentPath(editing.path)
    return (
      <FileEditor
        key={editing.path}
        hostId={hostId}
        path={editing.path}
        size={editing.size}
        onClose={() => setEditing(null)}
        onDirtyChange={setDirty}
        leaving={
          leaving
            ? {
                onDiscard: () => {
                  setLeaving(false)
                  setEditing(null)
                },
                onStay: () => {
                  setLeaving(false)
                  go(home)
                },
              }
            : null
        }
      />
    )
  }

  return (
    <section aria-label="Files" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <PathBar path={path} onGo={go} />
        <div className="flex flex-col items-end gap-1">
          <div className="flex gap-2">
            <Button variant="outline" {...{ [FALLBACK_FOCUS]: "" }} disabled={protectedDir !== undefined || uploading !== null} onClick={() => picker.current?.click()}>
              <Upload aria-hidden="true" />
              Upload
            </Button>
            <Button variant="outline" disabled={protectedDir !== undefined} onClick={() => setPending({ op: "mkdir" })}>
              <FolderPlus aria-hidden="true" />
              New folder
            </Button>
          </div>
          {protectedDir !== undefined && <span className="text-xs text-muted-foreground">{protectedDir}</span>}
        </div>
        <input
          ref={picker}
          type="file"
          className="sr-only"
          tabIndex={-1}
          aria-hidden="true"
          data-testid="upload-picker"
          onChange={(e) => {
            const file = e.target.files?.[0]
            e.target.value = ""
            if (file !== undefined) void startUpload(file)
          }}
        />
      </div>
      {uploading !== null && <UploadProgress up={uploading} />}
      {list.isPending ? (
        <div role="status" aria-label={`Loading ${path}`} className="flex flex-col gap-2">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-8 w-full" />
          ))}
        </div>
      ) : list.isError ? (
        <Alert variant="destructive" role="alert">
          <AlertTitle as="h3">{`Couldn't list ${path}`}</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            {errorMessage(list.error)}
            <Button size="sm" variant="outline" onClick={() => void list.refetch()}>
              Try again
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <>
          <Table aria-label={`Files in ${path}`}>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Size</TableHead>
                <TableHead>Mode</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead>Modified</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {path !== "/" && (
                <TableRow>
                  <TableCell colSpan={6}>
                    <Button variant="link" size="sm" className="h-auto p-0 font-mono" onClick={() => go(parentPath(path))}>
                      ../
                    </Button>
                  </TableCell>
                </TableRow>
              )}
              {entries.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="text-muted-foreground">
                    This folder is empty
                  </TableCell>
                </TableRow>
              )}
              {entries.map((e) => (
                <TableRow key={e.raw ?? e.name}>
                  <TableCell className="max-w-[28rem] break-all font-mono text-xs">
                    {e.type === "dir" && e.escaped !== true ? (
                      <Button variant="link" size="sm" className="h-auto whitespace-normal break-all p-0 text-start font-mono text-xs" onClick={() => go(childPath(path, e.name))}>
                        {shownName(e)}
                      </Button>
                    ) : (
                      shownName(e)
                    )}
                    {e.protected !== undefined && <span className="block font-sans text-muted-foreground">{e.protected}</span>}
                  </TableCell>
                  <TableCell className="whitespace-nowrap">{e.type === "dir" ? "" : formatSize(e.size)}</TableCell>
                  <TableCell className="font-mono text-xs">{e.mode}</TableCell>
                  <TableCell className="break-all">{`${e.owner}:${e.group}`}</TableCell>
                  <TableCell className="whitespace-nowrap">{modified(e.modified)}</TableCell>
                  <TableCell>
                    <EntryMenu entry={e} onPick={onPick} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {list.hasNextPage && (
            <div className="flex items-center gap-3">
              <span className="text-sm text-muted-foreground">{`${entries.length} entries shown; the folder holds more`}</span>
              <Button size="sm" variant="outline" disabled={list.isFetchingNextPage} onClick={() => void list.fetchNextPage()}>
                Show more
              </Button>
            </div>
          )}
        </>
      )}
      {pending !== null && (
        <OpDialog
          hostId={hostId}
          dir={path}
          pending={pending}
          onClose={() => setPending(null)}
          onDone={() => {
            setPending(null)
            refresh()
          }}
        />
      )}
    </section>
  )
}
