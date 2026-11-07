import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Field, MarkBadges, ReadFailure } from "./list-parts"
import { readProxyHostList } from "./list-replies"
import { NpmChange } from "./npm-change"
import { parseDomains, parseOptionalId, parsePort } from "./parse"
import { npmKey, type ProxyHostView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

type Scheme = "http" | "https"

interface Draft {
  domains: string
  scheme: Scheme
  host: string
  port: string
  certificateId: string
  accessListId: string
  sslForced: boolean
  hsts: boolean
  websockets: boolean
}

const EMPTY: Draft = { domains: "", scheme: "http", host: "", port: "", certificateId: "", accessListId: "", sslForced: false, hsts: false, websockets: false }

function idText(id: number): string {
  return id === 0 ? "" : String(id)
}

function draftOf(h: ProxyHostView): Draft {
  return {
    domains: h.domains.join(", "),
    scheme: h.forwardScheme === "https" ? "https" : "http",
    host: h.forwardHost,
    port: String(h.forwardPort),
    certificateId: idText(h.certificateId),
    accessListId: idText(h.accessListId),
    sslForced: h.sslForced,
    hsts: h.hsts,
    websockets: h.websockets,
  }
}

function namesOf(h: ProxyHostView): string {
  return h.domains.join(", ")
}

function options(h: ProxyHostView): string {
  const set: string[] = []
  if (h.sslForced) set.push("force SSL")
  if (h.hsts) set.push("HSTS")
  if (h.websockets) set.push("websockets")
  return set.join(", ")
}

function Flag({ id, label, checked, onChange }: { id: string; label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} onCheckedChange={(c) => onChange(c === true)} />
      <Label htmlFor={id}>{label}</Label>
    </div>
  )
}

// ProxyHostForm holds one opening's draft; it mounts when the dialog opens,
// so the next opening starts from its own initial values.
function ProxyHostForm({ initial, onPlan }: { initial: Draft; onPlan: (domains: string[], input: Record<string, unknown>) => void }) {
  const [draft, setDraft] = useState<Draft>(initial)
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((d) => ({ ...d, [key]: value }))

  const domains = parseDomains(draft.domains)
  const host = draft.host.trim()
  const port = parsePort(draft.port)
  const certificateId = parseOptionalId(draft.certificateId)
  const accessListId = parseOptionalId(draft.accessListId)
  const ready = domains.length > 0 && host !== "" && port !== null && certificateId !== null && accessListId !== null

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    onPlan(domains, {
      domains,
      forwardScheme: draft.scheme,
      forwardHost: host,
      forwardPort: port,
      certificateId,
      accessListId,
      sslForced: draft.sslForced,
      hsts: draft.hsts,
      websockets: draft.websockets,
    })
  }

  return (
    <form aria-label="Proxy host" className="flex flex-col gap-4" onSubmit={submit}>
      <Field id="proxyhost-domains" label="Domains" value={draft.domains} onChange={(v) => set("domains", v)} help="Separate several names with commas or spaces." />
      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium">Scheme</legend>
        <RadioGroup value={draft.scheme} onValueChange={(v) => set("scheme", v === "https" ? "https" : "http")} aria-label="Scheme">
          {(["http", "https"] as const).map((s) => (
            <div key={s} className="flex items-center gap-2">
              <RadioGroupItem id={`proxyhost-scheme-${s}`} value={s} />
              <Label htmlFor={`proxyhost-scheme-${s}`}>{s}</Label>
            </div>
          ))}
        </RadioGroup>
      </fieldset>
      <Field id="proxyhost-host" label="Forward host" value={draft.host} onChange={(v) => set("host", v)} />
      <Field id="proxyhost-port" label="Forward port" value={draft.port} invalid={draft.port !== "" && port === null} onChange={(v) => set("port", v)} />
      <Field id="proxyhost-cert" label="Certificate ID (optional)" value={draft.certificateId} invalid={certificateId === null} onChange={(v) => set("certificateId", v)} />
      <Field id="proxyhost-acl" label="Access list ID (optional)" value={draft.accessListId} invalid={accessListId === null} onChange={(v) => set("accessListId", v)} />
      <Flag id="proxyhost-ssl" label="Force SSL" checked={draft.sslForced} onChange={(v) => set("sslForced", v)} />
      <Flag id="proxyhost-hsts" label="HSTS" checked={draft.hsts} onChange={(v) => set("hsts", v)} />
      <Flag id="proxyhost-ws" label="Websockets" checked={draft.websockets} onChange={(v) => set("websockets", v)} />
      <DialogFooter>
        <Button type="submit" disabled={!ready}>Plan</Button>
      </DialogFooter>
    </form>
  )
}

function ProxyHostTable({ items, onEdit, onRemove }: { items: ProxyHostView[]; onEdit: (h: ProxyHostView) => void; onRemove: (h: ProxyHostView) => void }) {
  if (items.length === 0) return <EmptyState title="No proxy hosts" />
  return (
    <Table aria-label="Proxy hosts">
      <TableHeader>
        <TableRow>
          <TableHead>ID</TableHead>
          <TableHead>Domains</TableHead>
          <TableHead>Forwards to</TableHead>
          <TableHead>Certificate</TableHead>
          <TableHead>Access list</TableHead>
          <TableHead>Options</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((h) => (
          <TableRow key={h.id}>
            <TableCell>{h.id}</TableCell>
            <TableCell>{namesOf(h)}</TableCell>
            <TableCell className="font-mono">{`${h.forwardScheme}://${h.forwardHost}:${h.forwardPort}`}</TableCell>
            <TableCell>{h.certificateId === 0 ? "none" : h.certificateId}</TableCell>
            <TableCell>{h.accessListId === 0 ? "none" : h.accessListId}</TableCell>
            <TableCell>{options(h)}</TableCell>
            <TableCell>
              <MarkBadges reserved={h.reserved} unclaimed={h.unclaimed} />
            </TableCell>
            <TableCell>
              <div className="flex gap-2">
                <Button size="sm" variant="outline" aria-label={`Edit ${namesOf(h)}`} disabled={h.reserved !== ""} onClick={() => onEdit(h)}>
                  Edit
                </Button>
                <Button size="sm" variant="outline" aria-label={`Remove ${namesOf(h)}`} disabled={h.reserved !== ""} onClick={() => onRemove(h)}>
                  Remove
                </Button>
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// ProxyHostsTab lists the host's NPM proxy hosts and adds, edits or removes
// one through the change dialog.
export function ProxyHostsTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const [dialog, setDialog] = useState<{ editing: ProxyHostView | null } | null>(null)
  const query = useQuery({
    queryKey: npmKey(hostId, "proxyhost.list"),
    queryFn: () => readProxyHostList(hostId),
  })

  function plan(domains: string[], input: Record<string, unknown>) {
    const editing = dialog?.editing ?? null
    const names = domains.join(", ")
    setDialog(null)
    if (editing === null) {
      setPending({ action: "npm.proxyhost.add", title: `Add proxy host ${names}`, input })
    } else {
      setPending({ action: "npm.proxyhost.update", title: `Update proxy host ${names}`, input: { id: editing.id, ...input } })
    }
  }

  let content
  if (query.data === undefined) {
    content = query.isPending ? <Spinner aria-label="Loading proxy hosts" /> : <ReadFailure error={query.error} />
  } else {
    content = (
      <>
        {query.isError && <ReadFailure error={query.error} />}
        <ProxyHostTable
          items={query.data.items}
          onEdit={(h) => setDialog({ editing: h })}
          onRemove={(h) => setPending({ action: "npm.proxyhost.remove", title: `Remove proxy host ${namesOf(h)}`, input: { id: h.id } })}
        />
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex justify-end">
        <Button onClick={() => setDialog({ editing: null })}>Add proxy host</Button>
      </div>
      {content}
      {dialog !== null && (
        <Dialog open onOpenChange={(open) => (open ? undefined : setDialog(null))}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{dialog.editing === null ? "Add a proxy host" : "Edit a proxy host"}</DialogTitle>
              <DialogDescription>The next step shows what the proxy host changes before anything is applied.</DialogDescription>
            </DialogHeader>
            <ProxyHostForm initial={dialog.editing === null ? EMPTY : draftOf(dialog.editing)} onPlan={plan} />
          </DialogContent>
        </Dialog>
      )}
      <NpmChange
        hostId={hostId}
        action={pending?.action ?? "npm.proxyhost.add"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </div>
  )
}
