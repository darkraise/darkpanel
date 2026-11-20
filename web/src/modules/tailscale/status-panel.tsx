import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Spinner } from "darkraise-ui/components/spinner"
import { SquareTerminal } from "lucide-react"
import { ApiError, errorMessage, readResource } from "@/api/client"
import { Fact } from "@/components/visual/fact"
import { StatusPill } from "@/components/visual/status-pill"
import { backendPill, isTailscaleStatus, TAILSCALE_MODULE, type TailscaleStatus } from "./types"

const EMPTY = "—"

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

function Facts({ status }: { status: TailscaleStatus }) {
  const facts: { label: string; value: string; mono?: boolean }[] = [
    { label: "Version", value: orDash(status.version), mono: true },
    { label: "Tailnet", value: orDash(status.tailnet) },
    { label: "MagicDNS suffix", value: orDash(status.magicDNSSuffix), mono: true },
    { label: "DNS name", value: orDash(status.self?.dnsName), mono: true },
    { label: "Addresses", value: joined(status.self?.addresses), mono: true },
    { label: "Tags", value: joined(status.self?.tags), mono: true },
    { label: "Home DERP region", value: orDash(status.preferredDerp), mono: true },
  ]
  return (
    <div role="group" aria-label="Tailscale status" className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <div role="status" aria-live="polite" aria-label={`Node state: ${status.backendState}`} className="inline-flex">
          <StatusPill status={backendPill(status.backendState).status} label={status.backendState} />
        </div>
        <StatusPill status={status.runSSH ? "ok" : "off"} icon={SquareTerminal} label={status.runSSH ? "Tailscale SSH on" : "Tailscale SSH off"} />
      </div>
      <div className="flex flex-wrap gap-1">
        {facts.map((f) => (
          <Fact key={f.label} label={f.label} value={f.value} mono={f.mono} />
        ))}
      </div>
    </div>
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
          <AlertTitle as="h3">Tailscale reports problems</AlertTitle>
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
          <Facts status={status} />
        </CardContent>
      </Card>
    </div>
  )
}
