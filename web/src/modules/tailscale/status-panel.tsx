import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Spinner } from "darkraise-ui/components/spinner"
import { ApiError, errorMessage, readResource } from "@/api/client"
import { isTailscaleStatus, TAILSCALE_MODULE, type TailscaleStatus } from "./types"

const EMPTY = "—"

function stateVariant(state: string): "default" | "destructive" | "secondary" {
  if (state === "Running") return "default"
  if (state === "NeedsLogin" || state === "NeedsMachineAuth") return "destructive"
  return "secondary"
}

function joined(list: string[] | undefined): string {
  return list === undefined || list.length === 0 ? EMPTY : list.join(", ")
}

function orDash(v: string | undefined): string {
  return v === undefined || v === "" ? EMPTY : v
}

function readStatus(hostId: string): Promise<TailscaleStatus> {
  return readResource<unknown>(hostId, TAILSCALE_MODULE, "status").then((body) => {
    if (!isTailscaleStatus(body)) throw new Error("The host answered with an unexpected Tailscale status.")
    return body
  })
}

function Rows({ status }: { status: TailscaleStatus }) {
  const rows: { label: string; value: string }[] = [
    { label: "Version", value: orDash(status.version) },
    { label: "Tailnet", value: orDash(status.tailnet) },
    { label: "MagicDNS suffix", value: orDash(status.magicDNSSuffix) },
    { label: "DNS name", value: orDash(status.self?.dnsName) },
    { label: "Addresses", value: joined(status.self?.addresses) },
    { label: "Tags", value: joined(status.self?.tags) },
    { label: "Home DERP region", value: orDash(status.preferredDerp) },
    { label: "Tailscale SSH", value: status.runSSH ? "On" : "Off" },
  ]
  return (
    <dl aria-label="Tailscale status" className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
      <div className="contents">
        <dt className="text-muted-foreground">Backend state</dt>
        <dd>
          <Badge variant={stateVariant(status.backendState)} size="sm">
            {status.backendState}
          </Badge>
        </dd>
      </div>
      {rows.map((row) => (
        <div key={row.label} className="contents">
          <dt className="text-muted-foreground">{row.label}</dt>
          <dd>{row.value}</dd>
        </div>
      ))}
    </dl>
  )
}

// StatusPanel shows the host's Tailscale node: its state, names, addresses,
// home DERP region and SSH setting, and the warnings tailscaled reports.
export function StatusPanel({ hostId }: { hostId: string }) {
  const query = useQuery({
    queryKey: ["resource", hostId, "tailscale.status"],
    queryFn: () => readStatus(hostId),
  })

  if (query.isPending) return <Spinner aria-label="Loading the Tailscale status" />
  if (query.isError) {
    const message = query.error instanceof ApiError ? errorMessage(query.error) : query.error.message
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{message}</AlertDescription>
      </Alert>
    )
  }
  const status = query.data
  return (
    <div className="flex flex-col gap-4">
      {status.health.length > 0 && (
        <Alert variant="warning" role="status">
          <AlertTitle>Tailscale reports problems</AlertTitle>
          <AlertDescription>
            <ul aria-label="Health warnings" className="mt-1 list-disc pl-5">
              {status.health.map((message) => (
                <li key={message}>{message}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      <Card>
        <CardHeader>
          <CardTitle>Node</CardTitle>
        </CardHeader>
        <CardContent>
          <Rows status={status} />
        </CardContent>
      </Card>
    </div>
  )
}
