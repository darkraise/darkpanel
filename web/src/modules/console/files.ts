import { ApiError, rawRequest, request, upload } from "@/api/client"

// FileInfo is one directory entry as the agent lists it, never following a
// symbolic link. A name that is not UTF-8 arrives escaped (\xNN).
export interface FileInfo {
  name: string
  escaped?: boolean
  raw?: string
  type: "file" | "dir" | "symlink" | "other"
  size: number
  mode: string
  uid: number
  gid: number
  owner: string
  group: string
  modified: string
  target?: string
  protected?: string
}

export interface DirPage {
  path: string
  entries: FileInfo[]
  next?: string
  protected?: string
}

export interface FileWritten {
  sha256: string
  size: number
}

export interface TextFile {
  text: string
  sha256: string
}

export type FileChange = { path: string; to?: string; confirm?: string; mode?: string; uid?: number; gid?: number }

// The largest file the editor opens and saves (spec section 5).
export const MAX_TEXT = 1 << 20

export const CONFLICT = "Changed on the host since you opened it"
export const NOT_TEXT = "This file is not text or is over 1 MiB. Download it instead."
export const ESCAPED_REASON = "Its name is not UTF-8; change it in the terminal"

function filesUrl(hostId: string, op: string, query: Record<string, string> = {}): string {
  const q = new URLSearchParams(query).toString()
  return `/api/v1/hosts/${encodeURIComponent(hostId)}/files/${op}${q === "" ? "" : `?${q}`}`
}

export function listFiles(hostId: string, path: string, cursor: string): Promise<DirPage> {
  return request<DirPage>("GET", filesUrl(hostId, "list", cursor === "" ? { path } : { path, cursor }))
}

export async function readText(hostId: string, path: string): Promise<TextFile> {
  const res = await rawRequest("GET", filesUrl(hostId, "read", { path }))
  return { text: await res.text(), sha256: res.headers.get("Darkpanel-Sha256") ?? "" }
}

export async function writeText(hostId: string, path: string, text: string, sha256: string): Promise<FileWritten> {
  const res = await rawRequest("POST", filesUrl(hostId, "write", { path, sha256 }), { body: new Blob([new TextEncoder().encode(text)]) })
  return (await res.json()) as FileWritten
}

export function changeFile(hostId: string, op: "mkdir" | "rename" | "delete" | "chmod" | "chown", change: FileChange): Promise<void> {
  return request<void>("POST", filesUrl(hostId, op), change)
}

// askCodeFirst reads dir, which the file step-up window gates, so a code is
// asked in the page before a transfer the page cannot show a refusal of.
// The read's own answer does not matter.
async function askCodeFirst(hostId: string, dir: string, signal?: AbortSignal): Promise<void> {
  try {
    await rawRequest("GET", filesUrl(hostId, "read", { path: dir }), { signal })
  } catch (err) {
    if (err instanceof ApiError && err.code === "auth.step_up_required") throw err
    if (signal?.aborted === true) throw err
  }
}

// downloadFile has the browser stream the file to disk through a link, so a
// file of any size never sits in the tab's memory (UX-9). The code is asked
// first; a refusal after that shows as a failed download in the browser.
export async function downloadFile(hostId: string, path: string): Promise<void> {
  await askCodeFirst(hostId, parentPath(path))
  const a = document.createElement("a")
  a.href = filesUrl(hostId, "download", { path })
  a.download = baseName(path)
  document.body.appendChild(a)
  a.click()
  a.remove()
}

// uploadFile sends file into dir. The controller refuses an upload outside
// the step-up window before reading its body, and a browser still sending
// that body may see the refusal as a dropped connection; so the code is
// asked first.
export async function uploadFile(hostId: string, dir: string, file: File, onProgress: (sent: number) => void, signal: AbortSignal): Promise<FileWritten> {
  await askCodeFirst(hostId, dir, signal)
  return upload<FileWritten>(filesUrl(hostId, "upload", { path: childPath(dir, file.name) }), file, onProgress, signal)
}

// normalPath is an absolute path with no empty, "." or trailing segments;
// ".." stays, as the agent resolves it.
export function normalPath(path: string): string {
  const parts = path.split("/").filter((p) => p !== "" && p !== ".")
  return `/${parts.join("/")}`
}

export function childPath(dir: string, name: string): string {
  return dir === "/" ? `/${name}` : `${dir}/${name}`
}

export function parentPath(path: string): string {
  const at = path.lastIndexOf("/")
  return at <= 0 ? "/" : path.slice(0, at)
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

// crumbs are the path's ancestors from the root, each with its own path.
export function crumbs(path: string): { name: string; path: string }[] {
  const out = [{ name: "/", path: "/" }]
  let at = ""
  for (const part of path.split("/").filter((p) => p !== "")) {
    at += `/${part}`
    out.push({ name: part, path: at })
  }
  return out
}

export function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1 << 20) return `${(bytes / 1024).toFixed(1)} KiB`
  if (bytes < 1 << 30) return `${(bytes / (1 << 20)).toFixed(1)} MiB`
  return `${(bytes / (1 << 30)).toFixed(1)} GiB`
}

export const MODE_PATTERN = /^[0-7]{3,4}$/
