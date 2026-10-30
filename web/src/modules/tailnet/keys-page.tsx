import { useId, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "react-router"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Clipboard, ClipboardControl, ClipboardInput, ClipboardLabel, ClipboardTrigger } from "darkraise-ui/components/clipboard"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { changeError, changeInProgress, errorMessage, readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { formatTime } from "@/lib/format"
import { TAILNET_ACTION_VERSION, tailnetKey, type AuthKeyRow, type CreatedKey, type TailnetAccountStatus } from "./types"

type Phase = "closed" | "plan" | "created"

const MAX_EXPIRY_SECONDS = 7776000
const DEFAULT_EXPIRY = "86400"

function isCreatedKey(output: unknown): output is CreatedKey {
  if (typeof output !== "object" || output === null) return false
  const o = output as Record<string, unknown>
  return typeof o.key === "string" && typeof o.description === "string"
}

function Option({ id, label, checked, mono, onChange }: {
  id: string
  label: string
  checked: boolean
  mono?: boolean
  onChange: (checked: boolean) => void
}) {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} onCheckedChange={(c) => onChange(c === true)} />
      <Label htmlFor={id} className={mono ? "font-mono" : undefined}>
        {label}
      </Label>
    </div>
  )
}

function CreatedDialog({ created, open, onDone }: { created: CreatedKey | null; open: boolean; onDone: () => void }) {
  return (
    <Dialog open={open} onOpenChange={(next) => !next && onDone()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Auth key created</DialogTitle>
          <DialogDescription>{created === null ? "The key exists, but it cannot be shown." : "Copy the key now. It will not be shown again."}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-4">
          {created === null ? (
            <Alert variant="destructive" role="alert">
              <AlertDescription>The key was created, but the controller did not return it.</AlertDescription>
            </Alert>
          ) : (
            <>
              <Clipboard value={created.key}>
                <ClipboardLabel>{`Auth key ${created.description}`}</ClipboardLabel>
                <ClipboardControl>
                  <ClipboardInput className="font-mono" />
                  <ClipboardTrigger>Copy</ClipboardTrigger>
                </ClipboardControl>
              </Clipboard>
              {typeof created.expires === "string" && <p className="text-sm text-muted-foreground">{`Expires ${formatTime(created.expires)}.`}</p>}
            </>
          )}
          <DialogFooter>
            <Button onClick={onDone}>Done</Button>
          </DialogFooter>
        </div>
      </DialogContent>
    </Dialog>
  )
}

function KeysTable({ keys }: { keys: AuthKeyRow[] }) {
  if (keys.length === 0) return <EmptyState title="No auth keys" description="Darkpanel has not created any auth keys." />
  return (
    <Table aria-label="Auth keys">
      <TableHeader>
        <TableRow>
          <TableHead>Description</TableHead>
          <TableHead>Kind</TableHead>
          <TableHead>State</TableHead>
          <TableHead>Requested</TableHead>
          <TableHead>Expires</TableHead>
          <TableHead>Key ID</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {keys.map((k) => (
          <TableRow key={`${k.description}-${k.requestedAt}`}>
            <TableCell className="font-mono">{k.description}</TableCell>
            <TableCell>{k.kind}</TableCell>
            <TableCell>{k.state}</TableCell>
            <TableCell>{formatTime(k.requestedAt)}</TableCell>
            <TableCell>{formatTime(k.expires)}</TableCell>
            <TableCell className="font-mono">{k.keyId ?? ""}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function NotConnected() {
  return (
    <Alert role="status">
      <AlertDescription>
        No tailnet account is connected. <Link to="/tailnet">Connect an account</Link> first.
      </AlertDescription>
    </Alert>
  )
}

// KeysPage lists the auth keys Darkpanel created and creates a new one
// through the change dialog. The key is in the apply answer only, so it is
// held in this component's state, shown once, and never cached.
export function KeysPage() {
  const client = useQueryClient()
  const base = useId()
  const keys = useQuery({
    queryKey: tailnetKey("keys"),
    queryFn: () => readAction<{ keys: AuthKeyRow[] }>("tailnet.authkey.list", TAILNET_ACTION_VERSION, {}),
  })
  const account = useQuery({
    queryKey: tailnetKey("account"),
    queryFn: () => readAction<TailnetAccountStatus>("tailnet.account.get", TAILNET_ACTION_VERSION, {}),
  })
  const [phase, setPhase] = useState<Phase>("closed")
  const [tags, setTags] = useState<string[]>([])
  const [reusable, setReusable] = useState(false)
  const [ephemeral, setEphemeral] = useState(false)
  const [preauthorized, setPreauthorized] = useState(false)
  const [expiry, setExpiry] = useState(DEFAULT_EXPIRY)
  const [message, setMessage] = useState<string | null>(null)
  const [input, setInput] = useState<Record<string, unknown>>({})
  const [created, setCreated] = useState<CreatedKey | null>(null)

  function refresh() {
    void client.invalidateQueries({ queryKey: ["tailnet"] })
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (tags.length === 0) {
      setMessage("Choose at least one tag.")
      return
    }
    const typed = expiry.trim()
    const seconds = Number(typed)
    if (!/^[0-9]+$/.test(typed) || seconds < 1 || seconds > MAX_EXPIRY_SECONDS) {
      setMessage(`Enter a whole number of seconds from 1 to ${MAX_EXPIRY_SECONDS}.`)
      return
    }
    setMessage(null)
    setInput({
      tags,
      expirySeconds: seconds,
      ...(reusable && { reusable: true }),
      ...(ephemeral && { ephemeral: true }),
      ...(preauthorized && { preauthorized: true }),
    })
    setPhase("plan")
  }

  function applied(change: Change) {
    if (changeError(change) !== null || changeInProgress(change)) return
    setCreated(isCreatedKey(change.output) ? change.output : null)
    setPhase("created")
    refresh()
  }

  function done() {
    setCreated(null)
    setPhase("closed")
    refresh()
  }

  function toggleTag(tag: string, on: boolean) {
    setTags((cur) => (on ? [...cur, tag] : cur.filter((t) => t !== tag)))
  }

  function renderForm(status: TailnetAccountStatus) {
    if (!status.connected || status.account === undefined) return <NotConnected />
    return (
      <form aria-label="Create an auth key" className="flex flex-col gap-4" onSubmit={submit}>
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 text-sm font-medium">Tags</legend>
          {status.account.tagsAllowed.map((tag, i) => (
            <Option key={tag} id={`${base}-tag${i}`} label={tag} mono checked={tags.includes(tag)} onChange={(on) => toggleTag(tag, on)} />
          ))}
        </fieldset>
        <Option id={`${base}-reusable`} label="Reusable" checked={reusable} onChange={setReusable} />
        <Option id={`${base}-ephemeral`} label="Ephemeral" checked={ephemeral} onChange={setEphemeral} />
        <Option id={`${base}-preauthorized`} label="Pre-authorized" checked={preauthorized} onChange={setPreauthorized} />
        <div className="flex flex-col gap-2">
          <Label htmlFor={`${base}-expiry`}>Expiry (seconds)</Label>
          <Input id={`${base}-expiry`} inputMode="numeric" autoComplete="off" value={expiry} onChange={(e) => setExpiry(e.target.value)} />
        </div>
        {message !== null && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{message}</AlertDescription>
          </Alert>
        )}
        <div>
          <Button type="submit">Create key</Button>
        </div>
      </form>
    )
  }

  let formBody
  if (account.isPending) formBody = <Spinner aria-label="Loading the account" />
  else if (account.isError) {
    formBody = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(account.error)}</AlertDescription>
      </Alert>
    )
  } else formBody = renderForm(account.data)

  let keysBody
  if (keys.isPending) keysBody = <Spinner aria-label="Loading the auth keys" />
  else if (keys.isError) {
    keysBody = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(keys.error)}</AlertDescription>
      </Alert>
    )
  } else keysBody = <KeysTable keys={keys.data.keys} />

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Create an auth key</CardTitle>
        </CardHeader>
        <CardContent>{formBody}</CardContent>
      </Card>
      {keysBody}
      <ChangeDialog
        open={phase === "plan"}
        onOpenChange={(next) => {
          if (next) return
          setPhase((cur) => (cur === "plan" ? "closed" : cur))
          refresh()
        }}
        title="Create an auth key"
        target={{ name: "tailnet.authkey.create", version: TAILNET_ACTION_VERSION }}
        input={input}
        onApplied={applied}
      />
      <CreatedDialog created={created} open={phase === "created"} onDone={done} />
    </div>
  )
}
