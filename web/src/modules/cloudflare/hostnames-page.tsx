import { useId, useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readAction } from "@/api/client"
import { formatTime } from "@/lib/format"
import { CloudflareChange } from "./cloudflare-change"
import { CLOUDFLARE_ACTION_VERSION, cloudflareKey, type HostnameView, type TunnelView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

function words(value: string): string {
  return value.replace(/_/g, " ")
}

function healthVariant(health: HostnameView["health"]) {
  if (health === "ok") return "default"
  return health === "unknown" ? "secondary" : "destructive"
}

function HostnamesTable({ hostnames, tunnels, onPlan }: { hostnames: HostnameView[]; tunnels: TunnelView[]; onPlan: (pending: Pending) => void }) {
  if (hostnames.length === 0) return <EmptyState title="No hostnames yet" description="Add a hostname to publish a service through a tunnel." />
  const names = new Map(tunnels.map((t) => [t.id, t.name]))
  return (
    <Table aria-label="Hostnames">
      <TableHeader>
        <TableRow>
          <TableHead>Hostname</TableHead>
          <TableHead>Tunnel</TableHead>
          <TableHead>Service</TableHead>
          <TableHead>State</TableHead>
          <TableHead>Health</TableHead>
          <TableHead>Checked</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {hostnames.map((h) => (
          <TableRow key={h.hostname}>
            <TableCell>{h.hostname}</TableCell>
            <TableCell>{names.get(h.tunnel) ?? h.tunnel}</TableCell>
            <TableCell className="font-mono">{h.service}</TableCell>
            <TableCell>
              <Badge variant={h.phase === "active" ? "default" : "secondary"}>{words(h.phase)}</Badge>
            </TableCell>
            <TableCell>
              <Badge variant={healthVariant(h.health)}>{words(h.health)}</Badge>
            </TableCell>
            <TableCell>{formatTime(h.checkedAt ?? null)}</TableCell>
            <TableCell>
              <div className="flex justify-end">
                <Button
                  variant="outline"
                  size="sm"
                  aria-label={`Remove hostname ${h.hostname}`}
                  onClick={() => onPlan({ action: "cloudflare.hostname.remove", title: `Remove ${h.hostname}`, input: { hostname: h.hostname } })}
                >
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

function AddForm({ tunnels, onPlan }: { tunnels: TunnelView[]; onPlan: (pending: Pending) => void }) {
  const base = useId()
  const [chosen, setChosen] = useState("")
  const [hostname, setHostname] = useState("")
  const [service, setService] = useState("")
  const [noTLSVerify, setNoTLSVerify] = useState(false)
  const [message, setMessage] = useState<string | null>(null)

  const only = tunnels.length === 1 ? tunnels[0] : undefined
  const tunnel = chosen !== "" ? chosen : (only?.id ?? "")

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const host = hostname.trim()
    const svc = service.trim()
    if (tunnel === "" || host === "" || svc === "") {
      setMessage("Choose a tunnel and enter the hostname and the service.")
      return
    }
    setMessage(null)
    onPlan({
      action: "cloudflare.hostname.add",
      title: `Add ${host}`,
      input: { tunnel, hostname: host, service: svc, ...(noTLSVerify && { noTLSVerify: true }) },
    })
  }

  return (
    <form aria-label="Add hostname" className="flex flex-col gap-4" onSubmit={submit}>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium">Tunnel</legend>
        <RadioGroup value={tunnel} onValueChange={setChosen} aria-label="Tunnel" className="flex flex-wrap gap-4">
          {tunnels.map((t, i) => (
            <div key={t.id} className="flex items-center gap-2">
              <RadioGroupItem id={`${base}-tunnel${i}`} value={t.id} />
              <Label htmlFor={`${base}-tunnel${i}`} className="font-mono">
                {t.name}
              </Label>
            </div>
          ))}
        </RadioGroup>
      </fieldset>
      <div className="flex flex-col gap-2">
        <Label htmlFor={`${base}-hostname`}>Hostname</Label>
        <Input id={`${base}-hostname`} autoComplete="off" value={hostname} onChange={(e) => setHostname(e.target.value)} />
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor={`${base}-service`}>Service</Label>
        <Input id={`${base}-service`} autoComplete="off" className="font-mono" value={service} onChange={(e) => setService(e.target.value)} />
        <p className="text-sm text-muted-foreground">Use http:// or https:// with a host and port, tcp://, ssh:// or http_status:404.</p>
      </div>
      <div className="flex items-center gap-2">
        <Checkbox id={`${base}-notls`} checked={noTLSVerify} onCheckedChange={(c) => setNoTLSVerify(c === true)} />
        <Label htmlFor={`${base}-notls`}>Do not verify the origin&apos;s TLS certificate</Label>
      </div>
      {message !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      )}
      <div>
        <Button type="submit">Add hostname</Button>
      </div>
    </form>
  )
}

export function HostnamesPage() {
  const tunnels = useQuery({
    queryKey: cloudflareKey("tunnels"),
    queryFn: () => readAction<{ tunnels: TunnelView[] }>("cloudflare.tunnel.list", CLOUDFLARE_ACTION_VERSION, {}),
  })
  const hostnames = useQuery({
    queryKey: cloudflareKey("hostnames"),
    queryFn: () => readAction<{ hostnames: HostnameView[] }>("cloudflare.hostname.list", CLOUDFLARE_ACTION_VERSION, {}),
  })
  const [pending, setPending] = useState<Pending | null>(null)

  const failure = hostnames.isError ? hostnames.error : tunnels.isError ? tunnels.error : null
  if (failure !== null) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(failure)}</AlertDescription>
      </Alert>
    )
  }
  if (tunnels.data === undefined || hostnames.data === undefined) return <Spinner aria-label="Loading the hostnames" />
  const tunnelList = tunnels.data.tunnels

  return (
    <div className="flex flex-col gap-6">
      <Card>
        <CardHeader>
          <CardTitle>Hostnames</CardTitle>
        </CardHeader>
        <CardContent>
          <HostnamesTable hostnames={hostnames.data.hostnames} tunnels={tunnelList} onPlan={setPending} />
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Add a hostname</CardTitle>
        </CardHeader>
        <CardContent>
          {tunnelList.length === 0 ? <Link to="/cloudflare/tunnels">Create a tunnel first</Link> : <AddForm tunnels={tunnelList} onPlan={setPending} />}
        </CardContent>
      </Card>
      {pending !== null && <CloudflareChange action={pending.action} title={pending.title} input={pending.input} onClose={() => setPending(null)} />}
    </div>
  )
}
