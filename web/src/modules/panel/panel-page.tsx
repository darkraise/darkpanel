import { useId, useState, type FormEvent, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, listHosts } from "@/api/client"
import { PanelChange } from "./panel-change"
import { messageOf, readExposureStatus } from "./status"
import { panelStatusKey, type ExposureVia, type ExposureView } from "./types"

type Pending = null | { action: string; title: string; input: Record<string, unknown> }

const PORT_MIN = 1024
const PORT_MAX = 65535

const ROUTES: { value: ExposureVia; label: string }[] = [
  { value: "npm", label: "Nginx Proxy Manager" },
  { value: "tunnel", label: "Cloudflare tunnel" },
]

function routeLabel(via: ExposureVia): string {
  return via === "npm" ? "Nginx Proxy Manager" : "Cloudflare tunnel"
}

// listenPort is the whole number typed, or null when it is not one the
// controller accepts for a listener.
function listenPort(typed: string): number | null {
  if (!/^[0-9]+$/.test(typed)) return null
  const port = Number(typed)
  return port >= PORT_MIN && port <= PORT_MAX ? port : null
}

function ExposureRows({ exposure }: { exposure: ExposureView }) {
  const rows: { label: string; value: ReactNode }[] = [
    { label: "Route", value: routeLabel(exposure.via) },
    { label: "Hostname", value: exposure.hostname },
    ...(exposure.via === "tunnel" ? [{ label: "Tunnel", value: exposure.tunnel ?? "" }] : []),
    { label: "Network", value: exposure.subnet },
    { label: "Gateway", value: exposure.gateway },
    { label: "Proxy", value: exposure.proxy },
    { label: "Port", value: String(exposure.port) },
    { label: "State", value: <Badge variant={exposure.state === "open" ? "default" : "destructive"}>{exposure.state}</Badge> },
  ]
  return (
    <dl aria-label="Panel exposure" className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
      {rows.map((row) => (
        <div key={row.label} className="contents">
          <dt className="text-muted-foreground">{row.label}</dt>
          <dd className="break-all">{row.value}</dd>
        </div>
      ))}
    </dl>
  )
}

function ExposeForm({ hostId, onPlan }: { hostId: string; onPlan: (pending: NonNullable<Pending>) => void }) {
  const base = useId()
  const [via, setVia] = useState<ExposureVia>("npm")
  const [hostname, setHostname] = useState("")
  const [tunnel, setTunnel] = useState("")
  const [portText, setPortText] = useState("")
  const port = listenPort(portText)
  const portInvalid = portText !== "" && port === null
  const ready = hostname.trim() !== "" && (via !== "tunnel" || tunnel.trim() !== "") && !portInvalid

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    const host = hostname.trim().toLowerCase()
    onPlan({
      action: "panel.exposure.add",
      title: `Expose the panel at ${host}`,
      input: {
        via,
        hostname: host,
        hostId,
        ...(via === "tunnel" && { tunnel: tunnel.trim().toLowerCase() }),
        ...(port !== null && { port }),
      },
    })
  }

  return (
    <form aria-label="Expose the panel" className="flex flex-col gap-4" onSubmit={submit}>
      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium">Route</legend>
        <RadioGroup value={via} onValueChange={(value) => setVia(value as ExposureVia)} aria-label="Route">
          {ROUTES.map((r) => (
            <div key={r.value} className="flex items-center gap-2">
              <RadioGroupItem id={`${base}-via-${r.value}`} value={r.value} />
              <Label htmlFor={`${base}-via-${r.value}`}>{r.label}</Label>
            </div>
          ))}
        </RadioGroup>
      </fieldset>
      <div className="flex flex-col gap-2">
        <Label htmlFor={`${base}-hostname`}>Public hostname</Label>
        <Input id={`${base}-hostname`} autoComplete="off" value={hostname} onChange={(e) => setHostname(e.target.value)} />
      </div>
      {via === "tunnel" && (
        <div className="flex flex-col gap-2">
          <Label htmlFor={`${base}-tunnel`}>Tunnel id</Label>
          <Input id={`${base}-tunnel`} autoComplete="off" value={tunnel} onChange={(e) => setTunnel(e.target.value)} />
        </div>
      )}
      <div className="flex flex-col gap-2">
        <Label htmlFor={`${base}-port`}>Port (optional)</Label>
        <Input
          id={`${base}-port`}
          inputMode="numeric"
          autoComplete="off"
          value={portText}
          aria-invalid={portInvalid ? true : undefined}
          aria-describedby={portInvalid ? `${base}-port-help ${base}-port-error` : `${base}-port-help`}
          onChange={(e) => setPortText(e.target.value)}
        />
        <span id={`${base}-port-help`} className="text-xs text-muted-foreground">
          The port the controller serves the exposure on. Leave empty for 9443.
        </span>
        {portInvalid && (
          <span id={`${base}-port-error`} className="text-xs text-destructive">
            {`Enter a whole number from ${PORT_MIN} to ${PORT_MAX}.`}
          </span>
        )}
      </div>
      <div>
        <Button type="submit" disabled={!ready}>
          Review
        </Button>
      </div>
    </form>
  )
}

// PanelPage is the panel module's page of a host: the live exposure of the
// panel with its removal, or the form that exposes it. Only the controller's
// own host offers the form; the removal is offered on every host's page,
// because it does not depend on the host.
export function PanelPage({ hostId }: { hostId: string }) {
  // pending outlives the dialog's close, so its title stays while it animates closed.
  const [pending, setPending] = useState<Pending>(null)
  const [open, setOpen] = useState(false)
  const status = useQuery({ queryKey: panelStatusKey, queryFn: readExposureStatus })
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })

  function plan(next: NonNullable<Pending>) {
    setPending(next)
    setOpen(true)
  }

  function body() {
    if (status.isPending) return <Spinner aria-label="Loading the panel exposure" />
    if (status.isError) {
      return (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{messageOf(status.error)}</AlertDescription>
        </Alert>
      )
    }
    const exposure = status.data.exposure
    if (status.data.exposed && exposure !== null && exposure !== undefined) {
      return (
        <>
          <ExposureRows exposure={exposure} />
          <div>
            <Button
              variant="destructive"
              onClick={() =>
                plan({ action: "panel.exposure.remove", title: `Remove the panel exposure at ${exposure.hostname}`, input: {} })
              }
            >
              Remove exposure
            </Button>
          </div>
        </>
      )
    }
    if (hosts.isPending) return <Spinner aria-label="Loading the host" />
    if (hosts.isError) {
      return (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{messageOf(hosts.error)}</AlertDescription>
        </Alert>
      )
    }
    if (hosts.data.hosts.some((h) => h.id === hostId && h.isLocal)) return <ExposeForm hostId={hostId} onPlan={plan} />
    return <p>The panel can be exposed only from the controller&apos;s own host. Open the panel page of that host.</p>
  }

  return (
    <div className="flex flex-col gap-4">
      {body()}
      <PanelChange
        hostId={hostId}
        action={pending?.action ?? ""}
        title={pending?.title ?? ""}
        input={open ? (pending?.input ?? null) : null}
        onClose={() => setOpen(false)}
      />
    </div>
  )
}
