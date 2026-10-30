import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readAction } from "@/api/client"
import { CapabilityList } from "@/components/capability-list"
import { formatTime } from "@/lib/format"
import { CloudflareChange } from "./cloudflare-change"
import { CLOUDFLARE_ACTION_VERSION, cloudflareKey, type CloudflareAccountStatus, type CloudflareAccountView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

const TOKEN_PERMISSIONS = [
  "Account: Cloudflare Tunnel Write (shown as Cloudflare Tunnel: Edit in the dashboard)",
  "Zone: Zone Read on the zones you will use",
  "Zone: DNS Write on the zones you will use (shown as DNS: Edit)",
]

function ConnectForm({
  accountId,
  apiToken,
  onAccountId,
  onApiToken,
  onPlan,
}: {
  accountId: string
  apiToken: string
  onAccountId: (value: string) => void
  onApiToken: (value: string) => void
  onPlan: (pending: Pending) => void
}) {
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const id = accountId.trim()
    if (id === "" || apiToken === "") return
    onPlan({ action: "cloudflare.account.connect", title: "Connect Cloudflare", input: { accountId: id, apiToken } })
  }
  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <p>No Cloudflare account is connected.</p>
      <div className="flex flex-col gap-2">
        <Label htmlFor="cloudflare-account-id">Account ID</Label>
        <Input id="cloudflare-account-id" value={accountId} required autoComplete="off" onChange={(e) => onAccountId(e.target.value)} />
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="cloudflare-api-token">API token</Label>
        <Input
          id="cloudflare-api-token"
          type="password"
          value={apiToken}
          required
          autoComplete="off"
          onChange={(e) => onApiToken(e.target.value)}
        />
      </div>
      <div className="text-sm text-muted-foreground">
        <p>The token needs these permissions:</p>
        <ul aria-label="Required token permissions" className="mt-1 list-disc pl-5">
          {TOKEN_PERMISSIONS.map((permission) => (
            <li key={permission}>{permission}</li>
          ))}
        </ul>
      </div>
      <div>
        <Button type="submit">Connect</Button>
      </div>
    </form>
  )
}

function Connected({
  account,
  abandon,
  onAbandon,
  onPlan,
}: {
  account: CloudflareAccountView
  abandon: boolean
  onAbandon: (value: boolean) => void
  onPlan: (pending: Pending) => void
}) {
  const rows: { label: string; value: string; mono?: boolean }[] = [
    { label: "Account ID", value: account.accountId, mono: true },
    { label: "Connected", value: formatTime(account.connectedAt) },
  ]
  return (
    <>
      <dl aria-label="Cloudflare account" className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
        {rows.map((row) => (
          <div key={row.label} className="contents">
            <dt className="text-muted-foreground">{row.label}</dt>
            <dd className={row.mono === true ? "font-mono" : undefined}>{row.value}</dd>
          </div>
        ))}
      </dl>
      <div role="group" aria-label="Token capabilities" className="flex flex-col gap-2">
        <h3 className="text-sm font-medium">Token capabilities</h3>
        <CapabilityList capabilities={account.capabilities} />
      </div>
      <div className="flex flex-wrap items-center gap-4">
        <div className="flex items-center gap-2">
          <Checkbox id="cloudflare-abandon" checked={abandon} onCheckedChange={(checked) => onAbandon(checked === true)} />
          <Label htmlFor="cloudflare-abandon">Abandon pending cleanup</Label>
        </div>
        <Button
          variant="destructive"
          onClick={() =>
            onPlan({
              action: "cloudflare.account.disconnect",
              title: "Disconnect Cloudflare",
              input: abandon ? { abandonCleanup: true } : {},
            })
          }
        >
          Disconnect
        </Button>
      </div>
    </>
  )
}

/**
 * Shows the connected Cloudflare account and its token capabilities, or the
 * form that connects one, and plans either change through the change dialog.
 * The API token lives only in the input and is cleared when the dialog closes.
 */
export function AccountPanel() {
  const [accountId, setAccountId] = useState("")
  const [apiToken, setApiToken] = useState("")
  const [abandon, setAbandon] = useState(false)
  const [pending, setPending] = useState<Pending | null>(null)
  const query = useQuery({
    queryKey: cloudflareKey("account"),
    queryFn: () => readAction<CloudflareAccountStatus>("cloudflare.account.get", CLOUDFLARE_ACTION_VERSION, {}),
  })
  if (query.isPending) return <Spinner aria-label="Loading the Cloudflare account" />
  if (query.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  }
  const status = query.data
  function close() {
    setPending(null)
    setApiToken("")
    setAbandon(false)
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>Cloudflare account</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {status.connected && status.account !== undefined ? (
          <Connected account={status.account} abandon={abandon} onAbandon={setAbandon} onPlan={setPending} />
        ) : (
          <ConnectForm accountId={accountId} apiToken={apiToken} onAccountId={setAccountId} onApiToken={setApiToken} onPlan={setPending} />
        )}
      </CardContent>
      {pending !== null && <CloudflareChange action={pending.action} title={pending.title} input={pending.input} onClose={close} />}
    </Card>
  )
}
