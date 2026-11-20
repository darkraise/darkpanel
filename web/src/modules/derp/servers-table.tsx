import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Activity, KeyRound, MapPin, RadioTower, Trash2, CircleArrowUp } from "lucide-react"
import { Fact } from "@/components/visual/fact"
import { ResourceCard } from "@/components/visual/resource-card"
import type { Status } from "@/components/visual/status"
import { StatusPill } from "@/components/visual/status-pill"
import { formatTime } from "@/lib/format"
import type { DerpHealth, DerpServers, DerpServerView } from "./types"

export type ServerAction = "probe" | "region" | "upgrade" | "rotate" | "remove"

const HEALTH_STATUS = { ok: "ok", degraded: "warn", broken: "error" } as const satisfies Record<DerpHealth, Status>

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

// ServersTable shows each DERP server as a card with its health, facts and
// the actions it offers. It fetches nothing; the page owns the data and the
// actions.
export function ServersTable({ servers, onAction }: { servers: DerpServers; onAction: (action: ServerAction, server: DerpServerView) => void }) {
  if (servers.servers.length === 0) {
    return <EmptyState title="No DERP servers" description="Deploy a DERP server to a host to relay your tailnet's traffic through it." />
  }
  return (
    <section aria-label="DERP servers" className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
      {servers.servers.map((s) => {
        const outdated = s.derperVersion !== servers.availableVersion
        return (
          <ResourceCard key={s.hostId} icon={RadioTower} title={s.host} status={{ status: HEALTH_STATUS[s.health], label: s.health }}>
            <div className="flex flex-wrap gap-1">
              <Fact label="Mode" value={modeText(s)} />
              <Fact label="Server name" value={s.hostName} mono />
              <Fact label="Region" value={regionText(s)} />
              <Fact label="Version" value={s.derperVersion} mono />
              {outdated && <StatusPill status="info" icon={CircleArrowUp} label={`${servers.availableVersion} available`} />}
              <Fact label="Certificate" value={certificateText(s)} />
            </div>
            {s.reasons.length > 0 && (
              <ul aria-label={`Reasons for ${s.host}`} className="list-disc pl-4 text-sm">
                {s.reasons.map((r) => (
                  <li key={r}>{r}</li>
                ))}
              </ul>
            )}
            {s.error !== "" && <p className="text-sm text-destructive-text">{`Last check failed: ${s.error}`}</p>}
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" aria-label={`Probe ${s.host}`} onClick={() => onAction("probe", s)}>
                <Activity aria-hidden />
                Probe
              </Button>
              <Button variant="outline" size="sm" aria-label={`Region of ${s.host}`} onClick={() => onAction("region", s)}>
                <MapPin aria-hidden />
                Region
              </Button>
              {outdated && (
                <Button variant="outline" size="sm" aria-label={`Upgrade ${s.host}`} onClick={() => onAction("upgrade", s)}>
                  <CircleArrowUp aria-hidden />
                  Upgrade
                </Button>
              )}
              {s.mode === "ip" && (
                <Button variant="outline" size="sm" aria-label={`Rotate certificate of ${s.host}`} onClick={() => onAction("rotate", s)}>
                  <KeyRound aria-hidden />
                  Rotate certificate
                </Button>
              )}
              <Button variant="destructive" size="sm" aria-label={`Remove ${s.host}`} onClick={() => onAction("remove", s)}>
                <Trash2 aria-hidden />
                Remove
              </Button>
            </div>
          </ResourceCard>
        )
      })}
    </section>
  )
}
