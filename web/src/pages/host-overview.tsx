import { useId, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { Clock, Cpu, ShieldCheck, ShieldOff } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readAction, readResource } from "@/api/client"
import type { Host, UpdateStatus } from "@/api/types"
import { Gauge } from "@/components/visual/gauge"
import { InfoTip } from "@/components/visual/info-tip"
import { StatTile } from "@/components/visual/stat-tile"
import type { Status } from "@/components/visual/status"
import { StatusDonut } from "@/components/visual/status-donut"
import { StatusDot } from "@/components/visual/status-dot"
import { stateVariant } from "@/lib/badges"
import { formatTime, formatUptime } from "@/lib/format"
import { focusableScroller } from "@/lib/table"
import { DOCKER_MODULE, dockerKey, type DockerContainer } from "@/modules/docker/types"
import { FIREWALL_MODULE, statusQueryKey, type FirewallStatus } from "@/modules/firewall/types"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { Tile, TileSkeleton } from "@/modules/system/tile"
import type { RebootState, Updates } from "@/modules/system/types"
import { TAILSCALE_MODULE, type TailscaleStatus } from "@/modules/tailscale/types"
import { useCanOnHost } from "@/session/can"
import { HostRemoval } from "./host-removal"
import { UpdateNowButton } from "./update-now-dialog"
import { UpdatePolicyButton } from "./update-policy-dialog"

const ROW = "grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4"
const DASH = "—"

function OverviewCard({ title, children }: { title: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id}>
      <Card>
        <CardHeader>
          <CardTitle>
            <h2 id={id}>{title}</h2>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">{children}</CardContent>
      </Card>
    </section>
  )
}

const gib = (bytes: number) => Math.round((bytes / 2 ** 30) * 10) / 10

// Unavailable is a read or module that gives a tile nothing to show: the
// error icon, and the reason behind a tip.
function Unavailable({ what, reason }: { what: string; reason: string | null }) {
  return (
    <div className="flex items-center gap-1 text-sm">
      <StatusDot status="error" label={`${what} unavailable`} />
      <span aria-hidden>Unavailable</span>
      {reason !== null && <InfoTip label={`Why ${what.toLowerCase()} is unavailable`}>{reason}</InfoTip>}
    </div>
  )
}

function Muted({ children }: { children: ReactNode }) {
  return <p className="text-muted-foreground text-sm">{children}</p>
}

function lastSeen(host: Host): string {
  return host.lastSeenAt === null ? "Never seen" : `Last seen ${formatTime(host.lastSeenAt)}`
}

// The first row: memory, disk and swap gauges, then load per core and uptime,
// all from one hostinfo summary read; offline, last seen stands in for them.
function FigureRow({ host }: { host: Host }) {
  const summary = useQuery({
    queryKey: ["resource", host.id, "hostinfo.summary"],
    queryFn: () => readResource<Summary>(host.id, "hostinfo", "summary"),
    enabled: host.online,
  })
  const seen = <Muted>{lastSeen(host)}</Muted>
  const s = summary.data
  const gauge = (title: string, figure: (s: Summary) => ReactNode) => (
    <Tile title={title} greyed={!host.online}>
      {!host.online && seen}
      {host.online && summary.isPending && <TileSkeleton />}
      {host.online && summary.isError && <Unavailable what={title} reason={errorMessage(summary.error)} />}
      {host.online && s !== undefined && <div className="flex justify-center">{figure(s)}</div>}
    </Tile>
  )
  const load = !host.online || s === undefined || s.cpu.logicalCount <= 0 ? DASH : (s.load.one / s.cpu.logicalCount).toFixed(2)
  return (
    <div className={ROW}>
      {gauge("Memory", ({ memory: m }) => <Gauge value={gib(m.totalBytes - m.availableBytes)} max={gib(m.totalBytes)} label="Memory" unit="GiB" />)}
      {gauge("Disk", ({ rootFs: d }) => <Gauge value={gib(d.totalBytes - d.availableBytes)} max={gib(d.totalBytes)} label="Disk" unit="GiB" />)}
      {gauge("Swap", ({ swap: w }) =>
        w.totalBytes === 0 ? <Muted>No swap</Muted> : <Gauge value={gib(w.totalBytes - w.freeBytes)} max={gib(w.totalBytes)} label="Swap" unit="GiB" />,
      )}
      <div className="flex flex-col gap-4">
        <StatTile icon={Cpu} figure={load} caption="Load per core" />
        <StatTile icon={Clock} figure={!host.online || s === undefined ? DASH : formatUptime(s.uptimeSeconds)} caption="Uptime" />
      </div>
    </div>
  )
}

type ModuleGate = { kind: "absent" } | { kind: "off" } | { kind: "unavailable"; reason: string | null } | { kind: "on" }

function gate(host: Host, id: string): ModuleGate {
  const m = host.modules.find((x) => x.id === id)
  if (m === undefined) return { kind: "absent" }
  if (!m.enabled) return { kind: "off" }
  if (!m.available) return { kind: "unavailable", reason: m.reason }
  return { kind: "on" }
}

// GatedTile is one service tile. It is a region in every state, so the row
// keeps its shape: offline, not enabled, unavailable, loading, failed or read.
// Offline, a value read earlier in the session stays with the offline pill.
function GatedTile({
  host,
  module,
  title,
  absent,
  status,
  offlineStatus,
  read,
  children,
}: {
  host: Host
  module: string
  title: string
  absent?: ReactNode
  status?: { status: Status; label: string }
  offlineStatus?: { status: Status; label: string }
  read: { pending: boolean; error: unknown; known: boolean }
  children?: ReactNode
}) {
  const g = gate(host, module)
  let body: ReactNode
  let pill: { status: Status; label: string } | undefined
  if (!host.online) {
    body = read.known ? children : <Muted>{lastSeen(host)}</Muted>
    pill = read.known ? (offlineStatus ?? status) : undefined
  } else if (g.kind === "absent") body = absent ?? <Muted>Not enabled</Muted>
  else if (g.kind === "off") body = <Muted>Not enabled</Muted>
  else if (g.kind === "unavailable") body = <Unavailable what={title} reason={g.reason} />
  else if (read.pending) body = <TileSkeleton />
  else if (read.error !== null) body = <Unavailable what={title} reason={errorMessage(read.error)} />
  else {
    body = children
    pill = status
  }
  return (
    <Tile title={title} status={pill} greyed={!host.online}>
      {body}
    </Tile>
  )
}

function readable(host: Host, module: string): boolean {
  return host.online && gate(host, module).kind === "on"
}

function UpdatesTile({ host }: { host: Host }) {
  const enabled = readable(host, "system")
  const updates = useQuery({ queryKey: ["resource", host.id, "system.updates"], queryFn: () => readResource<Updates>(host.id, "system", "updates"), enabled })
  const reboot = useQuery({ queryKey: ["resource", host.id, "system.reboot"], queryFn: () => readResource<RebootState>(host.id, "system", "reboot"), enabled })
  const u = updates.data
  return (
    <GatedTile
      host={host}
      module="system"
      title="Updates"
      absent={
        <div className="flex flex-col items-start gap-1 text-sm">
          <span>Agent update needed</span>
          <span className="text-muted-foreground">This agent predates package management.</span>
        </div>
      }
      status={reboot.data?.required ? { status: "warn", label: "Reboot needed" } : undefined}
      read={{ pending: updates.isPending, error: updates.error, known: u !== undefined }}
    >
      {u !== undefined && (
        <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1">
          <span>
            <span className="font-mono text-2xl font-semibold tabular-nums">{u.upgradable.length}</span> <span className="text-muted-foreground text-sm">pending</span>
          </span>
          <span>
            <span className="font-mono text-2xl font-semibold tabular-nums">{u.upgradable.filter((p) => p.security).length}</span>{" "}
            <span className="text-muted-foreground text-sm">security</span>
          </span>
        </div>
      )}
    </GatedTile>
  )
}

function containerSegments(containers: DockerContainer[]) {
  const count = (pred: (c: DockerContainer) => boolean) => containers.filter(pred).length
  const running = count((c) => c.state === "running")
  const exited = count((c) => c.state === "exited" || c.state === "dead")
  return [
    { status: "ok" as const, label: "Running", value: running },
    { status: "error" as const, label: "Exited", value: exited },
    { status: "off" as const, label: "Stopped", value: containers.length - running - exited },
  ]
}

function DockerTile({ host }: { host: Host }) {
  const containers = useQuery({
    queryKey: dockerKey(host.id, "containers"),
    queryFn: () => readResource<{ containers: DockerContainer[] }>(host.id, DOCKER_MODULE, "containers"),
    enabled: readable(host, DOCKER_MODULE),
  })
  return (
    <GatedTile host={host} module={DOCKER_MODULE} title="Docker" read={{ pending: containers.isPending, error: containers.error, known: containers.data !== undefined }}>
      {containers.data !== undefined && <StatusDonut label="Containers" segments={containerSegments(containers.data.containers)} />}
    </GatedTile>
  )
}

function FirewallTile({ host }: { host: Host }) {
  const status = useQuery({
    queryKey: statusQueryKey(host.id),
    queryFn: () => readResource<FirewallStatus>(host.id, FIREWALL_MODULE, "status"),
    enabled: readable(host, FIREWALL_MODULE),
  })
  const f = status.data
  const pill: { status: Status; label: string } | undefined =
    f === undefined ? undefined : !f.supported ? { status: "error", label: "Unsupported" } : f.active ? { status: "ok", label: "Active" } : { status: "warn", label: "Inactive" }
  const Shield = f?.active ? ShieldCheck : ShieldOff
  return (
    <GatedTile
      host={host}
      module={FIREWALL_MODULE}
      title="Firewall"
      status={pill}
      offlineStatus={{ status: "off", label: "Last known" }}
      read={{ pending: status.isPending, error: status.error, known: f !== undefined }}
    >
      {f !== undefined && (
        <span className="flex items-center gap-2 text-sm">
          <Shield aria-hidden className={f.active ? "text-success-text size-5" : "text-muted-foreground size-5"} />
          <span className="font-mono tabular-nums">{f.rules.length}</span> {f.rules.length === 1 ? "rule" : "rules"}
        </span>
      )}
    </GatedTile>
  )
}

function tailscalePill(state: string): { status: Status; label: string } {
  switch (state) {
    case "Running":
      return { status: "ok", label: "Connected" }
    case "NeedsLogin":
    case "NeedsMachineAuth":
      return { status: "warn", label: "Needs login" }
    case "Stopped":
      return { status: "off", label: "Stopped" }
    default:
      return { status: "info", label: state }
  }
}

function TailscaleTile({ host }: { host: Host }) {
  const status = useQuery({
    queryKey: ["resource", host.id, "tailscale.status"],
    queryFn: () => readResource<TailscaleStatus>(host.id, TAILSCALE_MODULE, "status"),
    enabled: readable(host, TAILSCALE_MODULE),
  })
  const t = status.data
  return (
    <GatedTile
      host={host}
      module={TAILSCALE_MODULE}
      title="Tailscale"
      status={t === undefined ? undefined : tailscalePill(t.backendState)}
      offlineStatus={{ status: "off", label: "Unknown" }}
      read={{ pending: status.isPending, error: status.error, known: t !== undefined }}
    >
      {t !== undefined && <span className="font-mono text-sm tabular-nums break-all">{t.self?.addresses[0] ?? DASH}</span>}
    </GatedTile>
  )
}

// AgentTile folds the agent's facts and its update actions into one tile.
function AgentTile({ host }: { host: Host }) {
  const admin = useCanOnHost("admin")
  return (
    <Tile
      title="Agent"
      status={!host.supported && host.agentVersion !== null ? { status: "error", label: "Unsupported" } : undefined}
      actions={
        admin && (
          <>
            <UpdateNowButton host={host} label="Update agent" size="sm" disabled={!host.online} />
            <UpdatePolicyButton host={host} label="Change policy" size="sm" />
          </>
        )
      }
    >
      <span className="font-mono text-sm tabular-nums break-all">{host.agentVersion ?? "Not reported"}</span>
      {host.protocolVersion !== null && <span className="text-muted-foreground text-sm">{`Protocol ${host.protocolVersion}`}</span>}
    </Tile>
  )
}

function UpdateHistory({ hostId }: { hostId: string }) {
  const status = useQuery({
    queryKey: ["update-status", hostId],
    queryFn: () => readAction<UpdateStatus>("core.update.status", 1, {}, hostId),
  })
  if (status.isPending) return <Spinner aria-label="Loading the update history" />
  if (status.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(status.error)}</AlertDescription>
      </Alert>
    )
  }
  if (status.data.transactions.length === 0) return <EmptyState title="No updates yet" />
  return (
    <div className="@container">
      <div className="hidden overflow-x-auto @min-[40rem]:block">
        {/* darkraise-ui lets table cells break anywhere, so without a minimum width
            a narrow table squeezes its words apart instead of scrolling. */}
        <Table aria-label="Update history" className="min-w-[40rem]" ref={focusableScroller("Update history")}>
          <TableHeader>
            <TableRow>
              <TableHead>Started</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Error</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {status.data.transactions.map((t) => (
              <TableRow key={t.id}>
                <TableCell>{formatTime(t.startedAt)}</TableCell>
                <TableCell className="whitespace-nowrap">{t.kind}</TableCell>
                <TableCell className="whitespace-normal break-all">{`${t.fromVersion} → ${t.toVersion}`}</TableCell>
                <TableCell className="whitespace-nowrap">
                  <Badge variant={stateVariant(t.state)} size="sm">
                    {t.state}
                  </Badge>
                </TableCell>
                <TableCell>{t.error?.message ?? "—"}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <ul aria-label="Update history" className="flex flex-col divide-y rounded-md border @min-[40rem]:hidden">
        {status.data.transactions.map((t) => (
          <li key={t.id} className="flex flex-col gap-1 px-3 py-2">
            <div className="flex flex-wrap items-center gap-2">
              <Badge variant={stateVariant(t.state)} size="sm">
                {t.state}
              </Badge>
              <span className="break-all">{`${t.fromVersion} → ${t.toVersion}`}</span>
            </div>
            <p className="text-xs text-muted-foreground">{`${formatTime(t.startedAt)} · ${t.kind}`}</p>
            {t.error !== null && <p className="text-sm wrap-anywhere">{`Error: ${t.error.message}`}</p>}
          </li>
        ))}
      </ul>
    </div>
  )
}

export function HostOverview({ host }: { host: Host }) {
  const admin = useCanOnHost("admin")
  return (
    <div className="flex flex-col gap-4">
      {/* The tiles are h3s under the page's h1; this keeps the levels from skipping one. */}
      <h2 className="sr-only">Overview</h2>
      <FigureRow host={host} />
      <div className={ROW}>
        <UpdatesTile host={host} />
        <DockerTile host={host} />
        <div className="flex flex-col gap-4">
          <FirewallTile host={host} />
          <TailscaleTile host={host} />
        </div>
        <AgentTile host={host} />
      </div>
      <OverviewCard title="Agent update history">
        {host.online ? (
          <UpdateHistory hostId={host.id} />
        ) : (
          <p className="text-sm text-muted-foreground">The update history is unavailable while the host is offline.</p>
        )}
      </OverviewCard>
      {admin && (
        <OverviewCard title="Enrollment and removal">
          {host.isLocal ? (
            <p className="text-sm text-muted-foreground">The controller's own host cannot be revoked, deleted or given a new enrollment token.</p>
          ) : (
            <div className="flex flex-wrap gap-2">
              <HostRemoval host={host} />
            </div>
          )}
        </OverviewCard>
      )}
    </div>
  )
}
