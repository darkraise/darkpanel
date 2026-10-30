import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readAction } from "@/api/client"
import { CapabilityList } from "@/components/capability-list"
import { formatTime } from "@/lib/format"
import { TailnetChange } from "./tailnet-change"
import { TAILNET_ACTION_VERSION, tailnetKey, type TailnetAccountStatus, type TailnetAccountView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

interface Draft {
  tailnet: string
  clientId: string
  clientSecret: string
  tags: string
}

const EMPTY: Draft = { tailnet: "", clientId: "", clientSecret: "", tags: "" }

const TAG = /^tag:\S+$/

function parseTags(text: string): string[] {
  return text.split(/[\s,]+/).filter((tag) => tag !== "")
}

function Field({ id, label, value, placeholder, type, invalid, describedBy, onChange }: {
  id: string
  label: string
  value: string
  placeholder?: string
  type?: string
  invalid?: boolean
  describedBy?: string
  onChange: (value: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input
        id={id}
        type={type}
        value={value}
        placeholder={placeholder}
        autoComplete="off"
        aria-invalid={invalid ? true : undefined}
        aria-describedby={describedBy}
        onChange={(e) => onChange(e.target.value)}
      />
    </div>
  )
}

function ConnectForm({ onPlan }: { onPlan: (pending: Pending) => void }) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const tags = parseTags(draft.tags)
  const malformed = tags.some((tag) => !TAG.test(tag))
  const ready =
    draft.tailnet.trim() !== "" && draft.clientId.trim() !== "" && draft.clientSecret.trim() !== "" && tags.length > 0 && !malformed

  function set<K extends keyof Draft>(key: K, value: string) {
    setDraft((d) => ({ ...d, [key]: value }))
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const tailnet = draft.tailnet.trim()
    onPlan({
      action: "tailnet.account.connect",
      title: `Connect tailnet ${tailnet}`,
      input: { tailnet, clientId: draft.clientId.trim(), clientSecret: draft.clientSecret.trim(), tagsAllowed: tags },
    })
    setOpen(false)
    setDraft(EMPTY)
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>Connect</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Connect a tailnet</DialogTitle>
          <DialogDescription>
            The OAuth client secret is kept sealed and never shown again. The next step shows what connecting does before anything is saved.
          </DialogDescription>
        </DialogHeader>
        <form aria-label="Tailnet connection" className="flex flex-col gap-4" onSubmit={submit}>
          <Field id="tailnet-name" label="Tailnet" value={draft.tailnet} placeholder="example.com" onChange={(v) => set("tailnet", v)} />
          <Field id="tailnet-client-id" label="OAuth client ID" value={draft.clientId} onChange={(v) => set("clientId", v)} />
          <Field id="tailnet-client-secret" label="OAuth client secret" type="password" value={draft.clientSecret} onChange={(v) => set("clientSecret", v)} />
          <div className="flex flex-col gap-2">
            <Field
              id="tailnet-tags"
              label="Allowed tags"
              value={draft.tags}
              placeholder="tag:server, tag:darkpanel"
              invalid={malformed}
              describedBy="tailnet-tags-help"
              onChange={(v) => set("tags", v)}
            />
            <p id="tailnet-tags-help" className={malformed ? "text-sm text-destructive" : "text-sm text-muted-foreground"}>
              {malformed
                ? "Each tag must look like tag:name."
                : "The tags this client may give to devices and auth keys, separated by commas or spaces."}
            </p>
          </div>
          <DialogFooter>
            <Button type="submit" disabled={!ready}>Plan</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function Connected({ account, onPlan }: { account: TailnetAccountView; onPlan: (pending: Pending) => void }) {
  const [abandon, setAbandon] = useState(false)
  const rows: [string, string][] = [
    ["Tailnet", account.tailnet],
    ["OAuth client", account.clientId],
    ["Allowed tags", account.tagsAllowed.join(", ")],
    ["Connected", formatTime(account.connectedAt)],
  ]
  return (
    <>
      <dl aria-label="Tailnet account" className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
        {rows.map(([label, value]) => (
          <div key={label} className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      <div className="flex flex-col gap-2">
        <h3 className="text-sm font-medium">Capabilities</h3>
        <CapabilityList capabilities={account.capabilities} />
      </div>
      <div className="flex flex-wrap items-center gap-4">
        <Button
          variant="destructive"
          onClick={() =>
            onPlan({
              action: "tailnet.account.disconnect",
              title: `Disconnect ${account.tailnet}`,
              input: abandon ? { abandonCleanup: true } : {},
            })
          }
        >
          Disconnect
        </Button>
        <div className="flex items-center gap-2">
          <Checkbox id="tailnet-abandon" checked={abandon} onCheckedChange={(checked) => setAbandon(checked === true)} />
          <Label htmlFor="tailnet-abandon">Abandon pending cleanup</Label>
        </div>
      </div>
      {abandon && (
        <p className="text-sm text-muted-foreground">
          Disconnecting then deletes the credentials even if auth keys Darkpanel created are not yet deleted from the tailnet. Those keys stay there.
        </p>
      )}
    </>
  )
}

// AccountPanel shows the connected tailnet account and connects or
// disconnects it through the change dialog.
export function AccountPanel() {
  const [pending, setPending] = useState<Pending | null>(null)
  const query = useQuery({
    queryKey: tailnetKey("account"),
    queryFn: () => readAction<TailnetAccountStatus>("tailnet.account.get", TAILNET_ACTION_VERSION),
  })
  if (query.isPending) return <Spinner aria-label="Loading the tailnet account" />
  if (query.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  }
  const status = query.data
  return (
    <Card>
      <CardHeader>
        <CardTitle>Tailnet account</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {status.connected && status.account !== undefined ? (
          <Connected account={status.account} onPlan={setPending} />
        ) : (
          <>
            <p>No tailnet is connected.</p>
            <div>
              <ConnectForm onPlan={setPending} />
            </div>
          </>
        )}
      </CardContent>
      {pending !== null && (
        <TailnetChange action={pending.action} title={pending.title} input={pending.input} onClose={() => setPending(null)} />
      )}
    </Card>
  )
}
