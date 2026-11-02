import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { ApiError, errorMessage, readAction } from "@/api/client"
import { DERP_ACTION_VERSION, derpKey, isServerProbe, type ProbeResult, type ProbeSource, type RegionDiagnostics, type ServerProbe } from "./types"

function readProbe(hostId: string): Promise<ServerProbe> {
  return readAction<unknown>("derp.server.probe", DERP_ACTION_VERSION, { hostId }).then((body) => {
    if (!isServerProbe(body)) throw new Error("The controller answered with an unexpected probe result.")
    return body
  })
}

function measurementText(r: ProbeResult): string {
  if (r.ok) return `OK, ${r.latencyMs.toFixed(1)} ms`
  return r.error === "" ? "Failed" : `Failed: ${r.error}`
}

function yesNo(v: boolean): string {
  return v ? "Yes" : "No"
}

function ReportList({ label, name, items }: { label: string; name: string; items: string[] }) {
  if (items.length === 0) return null
  return (
    <div className="flex flex-col gap-1">
      <p className="text-sm font-medium">{label}</p>
      <ul aria-label={name} className="list-disc pl-4 text-sm">
        {items.map((item) => (
          <li key={item}>{item}</li>
        ))}
      </ul>
    </div>
  )
}

function RegionReport({ name, report }: { name: string; report: RegionDiagnostics }) {
  const title = `Connectivity diagnostics from ${name}`
  return (
    <section aria-label={title} className="flex flex-col gap-2">
      <h3 className="text-sm font-medium">{title}</h3>
      <ReportList label="Information" name={`Information from ${name}`} items={report.info} />
      <ReportList label="Warnings" name={`Warnings from ${name}`} items={report.warnings} />
      <ReportList label="Errors" name={`Errors from ${name}`} items={report.errors} />
    </section>
  )
}

function SourcesTable({ sources }: { sources: ProbeSource[] }) {
  return (
    <Table aria-label="Probe sources">
      <TableHeader>
        <TableRow>
          <TableHead>Source</TableHead>
          <TableHead>TLS</TableHead>
          <TableHead>STUN</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {sources.map((s) => (
          <TableRow key={s.source}>
            <TableCell>{s.name}</TableCell>
            <TableCell>{measurementText(s.tls)}</TableCell>
            <TableCell>{measurementText(s.stun)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// ProbePanel probes the DERP server of a host from the controller and its
// agents. Every read sends probes, so it does not refresh on window focus.
export function ProbePanel({ hostId }: { hostId: string }) {
  const query = useQuery({
    queryKey: derpKey("probe", hostId),
    queryFn: () => readProbe(hostId),
    refetchOnWindowFocus: false,
  })

  const again = (
    <div>
      <Button variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
        Probe again
      </Button>
    </div>
  )

  if (query.isPending) return <Spinner aria-label="Probing the server" />
  if (query.isError) {
    const message = query.error instanceof ApiError ? errorMessage(query.error) : query.error.message
    return (
      <div className="flex flex-col gap-3">
        <Alert variant="destructive" role="alert">
          <AlertDescription>{message}</AlertDescription>
        </Alert>
        {again}
      </div>
    )
  }

  const probe = query.data
  return (
    <div className="flex flex-col gap-3">
      <div>
        <Badge variant={probe.ready ? "default" : "secondary"}>{probe.ready ? "Ready to publish" : "Not ready"}</Badge>
      </div>
      <p>{probe.regionId === 0 ? "Region: not published" : `Region: ${probe.regionCode} (${probe.regionId})`}</p>
      <p>{`${probe.server.hostName}, derper ${probe.server.derperVersion}, ${probe.server.unitActive ? "unit active" : "unit inactive"}`}</p>
      <ReportList label="Warnings" name="Warnings" items={probe.warnings} />
      <SourcesTable sources={probe.sources} />
      {probe.sources.map((s) => s.region !== null && <RegionReport key={s.source} name={s.name} report={s.region} />)}
      {probe.sources.some((s) => s.region !== null) && (
        <p className="text-sm text-muted-foreground">
          Connectivity diagnostics show that the region answers a throwaway node key. They do not prove that two nodes can relay through it.
        </p>
      )}
      {probe.nodes.length === 0 ? (
        <EmptyState title="No nodes" />
      ) : (
        <Table aria-label="Nodes">
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>Address</TableHead>
              <TableHead>Visible</TableHead>
              <TableHead>Preferred region</TableHead>
              <TableHead>Relayed through this region</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {probe.nodes.map((n) => (
              <TableRow key={n.address}>
                <TableCell>{n.name}</TableCell>
                <TableCell className="font-mono">{n.address}</TableCell>
                <TableCell>{yesNo(n.visible)}</TableCell>
                <TableCell>{n.preferred === "" ? "Unknown" : n.preferred}</TableCell>
                <TableCell>{yesNo(n.observed)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {again}
    </div>
  )
}
