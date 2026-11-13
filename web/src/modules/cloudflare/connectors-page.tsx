import { useId, useState, type FormEvent } from "react"
import { useQuery, type UseQueryResult } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { HOSTS_QUERY_KEY, errorMessage, listHosts, readAction } from "@/api/client"
import type { Host } from "@/api/types"
import { CloudflareChange } from "./cloudflare-change"
import { connectorListKey, tunnelLabel, type Connector, type ConnectorList } from "./connector-types"
import { CLOUDFLARE_ACTION_VERSION, cloudflareKey, type TunnelView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
  hostId?: string
}

type Tunnels = UseQueryResult<{ tunnels: TunnelView[] }>

const NEW_TUNNEL = "new"

function runsCloudflare(host: Host): boolean {
  return host.modules.some((m) => m.id === "cloudflare" && m.enabled)
}

function ErrorAlert({ error }: { error: unknown }) {
  return (
    <Alert variant="destructive" role="alert">
      <AlertDescription>{errorMessage(error)}</AlertDescription>
    </Alert>
  )
}

function ConnectorsTable({ connectors, tunnels, onPlan }: { connectors: Connector[]; tunnels: TunnelView[]; onPlan: (pending: Pending) => void }) {
  return (
    <Table aria-label="Connectors" className="min-w-[40rem]">
      <TableHeader>
        <TableRow>
          <TableHead>Tunnel</TableHead>
          <TableHead>Container</TableHead>
          <TableHead>State</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {connectors.map((c) => {
          const label = tunnelLabel(c.tunnel, tunnels)
          return (
            <TableRow key={c.tunnel}>
              <TableCell className="wrap-anywhere">{label}</TableCell>
              <TableCell className="font-mono wrap-anywhere">{c.container}</TableCell>
              <TableCell className="whitespace-nowrap">
                <Badge size="sm" variant={c.state === "running" ? "default" : "secondary"}>
                  {c.state}
                </Badge>
              </TableCell>
              <TableCell>
                <div className="flex justify-end">
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={`Stop the connector of ${label}`}
                    onClick={() => onPlan({ action: "cloudflare.connector.stop", title: `Stop the connector of ${label}`, input: { tunnel: c.tunnel } })}
                  >
                    Stop
                  </Button>
                </div>
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

function ConnectorsCard({
  host,
  connectors,
  tunnels,
  onPlan,
}: {
  host: Host
  connectors: UseQueryResult<ConnectorList>
  tunnels: Tunnels
  onPlan: (pending: Pending) => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>{`Connectors on ${host.name}`}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {connectors.data === undefined ? (
          connectors.isPending ? (
            <Spinner aria-label="Loading the connectors" />
          ) : (
            <ErrorAlert error={connectors.error} />
          )
        ) : (
          <>
            {connectors.isError && <ErrorAlert error={connectors.error} />}
            {connectors.data.connectors.length === 0 ? (
              <EmptyState title="No connectors on this host" description="Deploy a connector to serve a tunnel from this host." />
            ) : (
              <ConnectorsTable connectors={connectors.data.connectors} tunnels={tunnels.data?.tunnels ?? []} onPlan={onPlan} />
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

function DeployForm({ host, tunnels, taken, onPlan }: { host: Host; tunnels: TunnelView[]; taken: Set<string>; onPlan: (pending: Pending) => void }) {
  const base = useId()
  const [chosen, setChosen] = useState(NEW_TUNNEL)
  const offered = tunnels.filter((t) => !taken.has(t.id))
  const tunnel = offered.some((t) => t.id === chosen) ? chosen : NEW_TUNNEL

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    onPlan({
      action: "cloudflare.connector.deploy",
      title: `Deploy a connector on ${host.name}`,
      input: { hostId: host.id, ...(tunnel !== NEW_TUNNEL && { tunnel }) },
    })
  }

  return (
    <form aria-label="Deploy a connector" className="flex flex-col gap-4" onSubmit={submit}>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium">Tunnel</legend>
        <RadioGroup value={tunnel} onValueChange={setChosen} aria-label="Tunnel" className="flex flex-wrap gap-4">
          <div className="flex items-center gap-2">
            <RadioGroupItem id={`${base}-new`} value={NEW_TUNNEL} />
            <Label htmlFor={`${base}-new`}>Create a new tunnel</Label>
          </div>
          {offered.map((t, i) => (
            <div key={t.id} className="flex items-center gap-2">
              <RadioGroupItem id={`${base}-tunnel${i}`} value={t.id} />
              <Label htmlFor={`${base}-tunnel${i}`} className="font-mono">
                {t.name}
              </Label>
            </div>
          ))}
        </RadioGroup>
      </fieldset>
      <div>
        <Button type="submit">Deploy connector</Button>
      </div>
    </form>
  )
}

function DeployCard({
  host,
  tunnels,
  connectors,
  onPlan,
}: {
  host: Host
  tunnels: Tunnels
  connectors: UseQueryResult<ConnectorList>
  onPlan: (pending: Pending) => void
}) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Deploy a connector</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {tunnels.isError && <ErrorAlert error={tunnels.error} />}
        {tunnels.isPending ? (
          <Spinner aria-label="Loading the tunnels" />
        ) : connectors.data !== undefined ? (
          <DeployForm
            host={host}
            tunnels={tunnels.data?.tunnels ?? []}
            taken={new Set(connectors.data.connectors.map((c) => c.tunnel))}
            onPlan={onPlan}
          />
        ) : connectors.isPending ? (
          <Spinner aria-label="Checking which tunnels have a connector" />
        ) : (
          <DeployForm host={host} tunnels={[]} taken={new Set()} onPlan={onPlan} />
        )}
      </CardContent>
    </Card>
  )
}

function HostConnectors({ host, tunnels }: { host: Host; tunnels: Tunnels }) {
  const connectors = useQuery({
    queryKey: connectorListKey(host.id),
    queryFn: () => readAction<ConnectorList>("cloudflare.connector.list", CLOUDFLARE_ACTION_VERSION, {}, host.id),
  })
  const [pending, setPending] = useState<Pending | null>(null)

  return (
    <>
      <ConnectorsCard host={host} connectors={connectors} tunnels={tunnels} onPlan={(p) => setPending({ ...p, hostId: host.id })} />
      <DeployCard host={host} tunnels={tunnels} connectors={connectors} onPlan={setPending} />
      {pending !== null && (
        <CloudflareChange action={pending.action} title={pending.title} input={pending.input} hostId={pending.hostId} onClose={() => setPending(null)} />
      )}
    </>
  )
}

function HostChoice({ hosts, value, onChange }: { hosts: Host[]; value: string; onChange: (id: string) => void }) {
  const base = useId()
  return (
    <Card>
      <CardHeader>
        <CardTitle>Host</CardTitle>
      </CardHeader>
      <CardContent>
        <RadioGroup value={value} onValueChange={onChange} aria-label="Host" className="flex flex-wrap gap-4">
          {hosts.map((h, i) => (
            <div key={h.id} className="flex min-w-0 items-center gap-2 wrap-anywhere">
              <RadioGroupItem id={`${base}-host${i}`} value={h.id} />
              <Label htmlFor={`${base}-host${i}`} className="min-w-0 wrap-anywhere">
                {h.name}
              </Label>
            </div>
          ))}
        </RadioGroup>
      </CardContent>
    </Card>
  )
}

export function ConnectorsPage() {
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const tunnels = useQuery({
    queryKey: cloudflareKey("tunnels"),
    queryFn: () => readAction<{ tunnels: TunnelView[] }>("cloudflare.tunnel.list", CLOUDFLARE_ACTION_VERSION, {}),
  })
  const [chosen, setChosen] = useState("")

  if (hosts.data === undefined) {
    return hosts.isPending ? <Spinner aria-label="Loading the hosts" /> : <ErrorAlert error={hosts.error} />
  }
  const eligible = hosts.data.hosts.filter(runsCloudflare)
  if (eligible.length === 0) {
    return (
      <>
        {hosts.isError && <ErrorAlert error={hosts.error} />}
        <EmptyState title="No host runs the Cloudflare module" description="Enable the module on a host to run a connector there." />
      </>
    )
  }
  const host = eligible.length === 1 ? eligible[0] : eligible.find((h) => h.id === chosen)

  return (
    <div className="flex flex-col gap-6">
      {hosts.isError && <ErrorAlert error={hosts.error} />}
      {eligible.length > 1 && <HostChoice hosts={eligible} value={chosen} onChange={setChosen} />}
      {host !== undefined && <HostConnectors key={host.id} host={host} tunnels={tunnels} />}
    </div>
  )
}
