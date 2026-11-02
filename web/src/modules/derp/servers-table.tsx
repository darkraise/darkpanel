import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { formatTime } from "@/lib/format"
import type { DerpHealth, DerpServers, DerpServerView } from "./types"

export type ServerAction = "probe" | "region" | "upgrade" | "rotate" | "remove"

const HEALTH_VARIANT = { ok: "default", degraded: "secondary", broken: "destructive" } as const satisfies Record<DerpHealth, string>

function modeText(server: DerpServerView): string {
  return server.mode === "letsencrypt" ? "Let's Encrypt" : "IP certificate"
}

function regionText(server: DerpServerView): string {
  return server.regionId === 0 ? "Not published" : `${server.regionCode} (${server.regionId})`
}

function certificateText(server: DerpServerView): string {
  if (server.mode !== "ip") return "Renewed by derper"
  return `Expires ${server.certNotAfter === null ? "—" : formatTime(server.certNotAfter)}`
}

// ServersTable lists the DERP servers with their health and the actions each
// one offers. It fetches nothing; the page owns the data and the actions.
export function ServersTable({ servers, onAction }: { servers: DerpServers; onAction: (action: ServerAction, server: DerpServerView) => void }) {
  if (servers.servers.length === 0) {
    return <EmptyState title="No DERP servers" description="Deploy a DERP server to a host to relay your tailnet's traffic through it." />
  }
  return (
    <Table aria-label="DERP servers">
      <TableHeader>
        <TableRow>
          <TableHead>Host</TableHead>
          <TableHead>Mode</TableHead>
          <TableHead>Server name</TableHead>
          <TableHead>Region</TableHead>
          <TableHead>Version</TableHead>
          <TableHead>Certificate</TableHead>
          <TableHead>Health</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {servers.servers.map((s) => {
          const outdated = s.derperVersion !== servers.availableVersion
          return (
            <TableRow key={s.hostId}>
              <TableCell className="font-medium">{s.host}</TableCell>
              <TableCell>{modeText(s)}</TableCell>
              <TableCell className="font-mono">{s.hostName}</TableCell>
              <TableCell>{regionText(s)}</TableCell>
              <TableCell>
                <div className="flex flex-wrap items-center gap-2">
                  <span>{s.derperVersion}</span>
                  {outdated && (
                    <Badge variant="secondary" size="sm">
                      {`${servers.availableVersion} available`}
                    </Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>{certificateText(s)}</TableCell>
              <TableCell>
                <div className="flex flex-col gap-1">
                  <div>
                    <Badge variant={HEALTH_VARIANT[s.health]} size="sm">
                      {s.health}
                    </Badge>
                  </div>
                  {s.reasons.length > 0 && (
                    <ul aria-label={`Reasons for ${s.host}`} className="list-disc pl-4 text-sm">
                      {s.reasons.map((r) => (
                        <li key={r}>{r}</li>
                      ))}
                    </ul>
                  )}
                  {s.error !== "" && <p className="text-sm text-destructive">{`Last check failed: ${s.error}`}</p>}
                </div>
              </TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-2">
                  <Button variant="outline" size="sm" aria-label={`Probe ${s.host}`} onClick={() => onAction("probe", s)}>
                    Probe
                  </Button>
                  <Button variant="outline" size="sm" aria-label={`Region of ${s.host}`} onClick={() => onAction("region", s)}>
                    Region
                  </Button>
                  {outdated && (
                    <Button variant="outline" size="sm" aria-label={`Upgrade ${s.host}`} onClick={() => onAction("upgrade", s)}>
                      Upgrade
                    </Button>
                  )}
                  {s.mode === "ip" && (
                    <Button variant="outline" size="sm" aria-label={`Rotate certificate of ${s.host}`} onClick={() => onAction("rotate", s)}>
                      Rotate certificate
                    </Button>
                  )}
                  <Button variant="destructive" size="sm" aria-label={`Remove ${s.host}`} onClick={() => onAction("remove", s)}>
                    Remove
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
