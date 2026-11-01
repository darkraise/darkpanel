import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { ApiError, errorMessage, readResource } from "@/api/client"
import { isTailscaleLatency, TAILSCALE_MODULE, type Measurement, type TailscaleLatency } from "./types"

function readLatency(hostId: string): Promise<TailscaleLatency> {
  return readResource<unknown>(hostId, TAILSCALE_MODULE, "latency").then((body) => {
    if (!isTailscaleLatency(body)) throw new Error("The host answered with an unexpected latency measurement.")
    return body
  })
}

function measurementText(m: Measurement): string {
  if (m.available) return `${m.medianMs.toFixed(1)} ms`
  return m.reason === "" ? "Unavailable" : `Unavailable: ${m.reason}`
}

// LatencyPanel measures the latency from the host to each DERP region. Every
// read sends probes, so it does not refresh on window focus.
export function LatencyPanel({ hostId }: { hostId: string }) {
  const query = useQuery({
    queryKey: ["resource", hostId, "tailscale.latency"],
    queryFn: () => readLatency(hostId),
    refetchOnWindowFocus: false,
  })

  if (query.isPending) return <Spinner aria-label="Measuring the latency" />
  if (query.isError) {
    const message = query.error instanceof ApiError ? errorMessage(query.error) : query.error.message
    return (
      <div className="flex flex-col gap-3">
        <Alert variant="destructive" role="alert">
          <AlertDescription>{message}</AlertDescription>
        </Alert>
        <div>
          <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
            Measure again
          </Button>
        </div>
      </div>
    )
  }
  if (query.data.regions.length === 0) return <EmptyState title="No DERP regions" />
  return (
    <div className="flex flex-col gap-3">
      <Table aria-label="DERP latency">
        <TableHeader>
          <TableRow>
            <TableHead>Region</TableHead>
            <TableHead>STUN latency</TableHead>
            <TableHead>DERP HTTPS probe latency</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {query.data.regions.map((r) => (
            <TableRow key={r.regionId}>
              <TableCell>{`${r.name} (${r.code})`}</TableCell>
              <TableCell>{measurementText(r.stun)}</TableCell>
              <TableCell>{measurementText(r.https)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <p className="text-sm text-muted-foreground">
        STUN latency is the median of three STUN binding requests from the host. The DERP HTTPS probe measures reaching the server, not a relayed peer path.
      </p>
      <div>
        <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
          Measure again
        </Button>
      </div>
    </div>
  )
}
