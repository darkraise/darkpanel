import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Server } from "lucide-react"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { errorMessage, readResource } from "@/api/client"
import type { Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import { ModuleIcon } from "@/components/visual/module-icon"
import { ResourceCard } from "@/components/visual/resource-card"
import type { Status } from "@/components/visual/status"
import { StatusDot } from "@/components/visual/status-dot"
import { StatusPill } from "@/components/visual/status-pill"
import { UsageBar } from "@/components/visual/usage-bar"
import { formatTime } from "@/lib/format"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { moduleRegistry } from "@/modules/registry"

type Usage = Pick<Summary, "memory" | "rootFs">

const MAX_IN_FLIGHT = 6
const READ_TIMEOUT_MS = 5000

class SummaryTimeout extends Error {
  constructor() {
    super("The host did not answer within 5 seconds.")
  }
}

// A fleet of slow agents must not open one request per card at once: summary
// reads share six slots across the page, and a slot is freed when its read
// settles, times out or is cancelled.
let active = 0
const waiting: (() => void)[] = []

function acquire(): Promise<void> {
  if (active < MAX_IN_FLIGHT) {
    active++
    return Promise.resolve()
  }
  return new Promise((resolve) => waiting.push(resolve))
}

function release() {
  const next = waiting.shift()
  if (next === undefined) active--
  else next()
}

function readUsage(hostId: string, outer: AbortSignal): Promise<Usage> {
  return acquire().then(
    () =>
      new Promise<Usage>((resolve, reject) => {
        const ctl = new AbortController()
        let done = false
        const settle = (finish: () => void) => {
          if (done) return
          done = true
          clearTimeout(timer)
          outer.removeEventListener("abort", onAbort)
          release()
          finish()
        }
        const onAbort = () => settle(() => (ctl.abort(), reject(outer.reason)))
        const timer = setTimeout(() => settle(() => (ctl.abort(), reject(new SummaryTimeout()))), READ_TIMEOUT_MS)
        if (outer.aborted) return onAbort()
        outer.addEventListener("abort", onAbort)
        readResource<Usage>(hostId, "hostinfo", "summary", ctl.signal).then(
          (usage) => settle(() => resolve(usage)),
          (err: unknown) => settle(() => reject(err)),
        )
      }),
  )
}

const gib = (bytes: number) => Math.round((bytes / 2 ** 30) * 10) / 10

function UsageFigures({ hostId }: { hostId: string }) {
  const usage = useQuery({
    queryKey: ["resource", hostId, "hostinfo.summary"],
    queryFn: ({ signal }) => readUsage(hostId, signal),
    staleTime: 30_000,
    refetchInterval: 60_000,
    retry: false,
  })
  if (usage.isPending) {
    return (
      <div aria-busy="true" className="flex flex-col gap-3">
        <span className="sr-only">Loading the usage</span>
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-full" />
      </div>
    )
  }
  if (usage.isError) {
    return (
      <div className="flex items-center gap-1 text-sm">
        <StatusDot status="error" label="Summary unavailable" />
        <span aria-hidden>Summary unavailable</span>
        <InfoTip label="Why the summary is unavailable">{errorMessage(usage.error)}</InfoTip>
      </div>
    )
  }
  const { memory, rootFs } = usage.data
  return (
    <>
      <UsageBar used={gib(memory.totalBytes - memory.availableBytes)} total={gib(memory.totalBytes)} label="Memory" unit="GiB" />
      <UsageBar used={gib(rootFs.totalBytes - rootFs.availableBytes)} total={gib(rootFs.totalBytes)} label="Disk" unit="GiB" />
    </>
  )
}

function hostStatus(host: Host): { status: Status; label: string } {
  if (host.state === "pending") return { status: "info", label: "Pending" }
  if (host.state === "revoked") return { status: "error", label: "Revoked" }
  return host.online ? { status: "ok", label: "Online" } : { status: "error", label: "Offline" }
}

function moduleLabel(id: string): string {
  return moduleRegistry.find((m) => m.id === id)?.label ?? id
}

function ModuleIcons({ host }: { host: Host }) {
  const enabled = host.modules.filter((m) => m.enabled)
  if (enabled.length === 0) return null
  return (
    <ul aria-label="Modules" className="flex flex-wrap gap-2">
      {enabled.map((m) => {
        const name = m.available ? moduleLabel(m.id) : `${moduleLabel(m.id)} (unavailable)`
        return (
          <li key={m.id} title={m.available || m.reason === null ? name : `${name}: ${m.reason}`}>
            {/* The list item's text names the module, so the icon's own label stays out of the accessibility tree. */}
            <span aria-hidden>
              <ModuleIcon module={m.id} dimmed={!m.available} />
            </span>
            <span className="sr-only">{name}</span>
          </li>
        )
      })}
    </ul>
  )
}

export function HostCard({ host }: { host: Host }) {
  const reads = host.state === "enrolled" && host.online
  return (
    <ResourceCard
      icon={Server}
      title={host.name}
      status={hostStatus(host)}
      actions={
        <Button asChild variant="outline" size="sm">
          <Link to={`/hosts/${encodeURIComponent(host.id)}`} aria-label={`Open ${host.name}`}>
            Open
          </Link>
        </Button>
      }
      figures={
        <>
          <div className="flex flex-wrap items-center gap-1">
            {host.os !== null && <Badge variant="outline">{host.os}</Badge>}
            {host.arch !== null && <Badge variant="outline">{host.arch}</Badge>}
            {host.isLocal && <Badge variant="secondary">This controller</Badge>}
            {!host.supported && host.agentVersion !== null && <StatusPill status="error" label="Unsupported" />}
            {host.ingestion !== null && !host.ingestion.ok && (
              <span className="inline-flex items-center">
                <StatusPill status="warn" label="Journal ingestion failing" />
                {host.ingestion.error !== null && <InfoTip label="Why journal ingestion fails">{host.ingestion.error}</InfoTip>}
              </span>
            )}
          </div>
          {reads ? (
            <UsageFigures hostId={host.id} />
          ) : (
            <span className="text-muted-foreground text-sm">
              {host.lastSeenAt === null ? "Never seen" : `Last seen ${formatTime(host.lastSeenAt)}`}
            </span>
          )}
        </>
      }
    >
      <ModuleIcons host={host} />
    </ResourceCard>
  )
}
