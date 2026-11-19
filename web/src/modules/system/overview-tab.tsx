import { useEffect, useId, useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { CalendarClock, CircleCheck, Download, RefreshCw, RotateCcw, Server, ShieldAlert, Wrench } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Switch } from "darkraise-ui/components/switch"
import { readResource, request } from "@/api/client"
import type { Change, Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import { StatTile } from "@/components/visual/stat-tile"
import { formatTime } from "@/lib/format"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { UpdateNowButton } from "@/pages/update-now-dialog"
import { DockerTile } from "./docker-tile"
import { RebootTile } from "./reboot-tile"
import { SystemChange, type PendingChange } from "./system-change"
import { Tile, TileError, TileSkeleton } from "./tile"
import { SYSTEM_MODULE, systemChangesKey, type AutoUpdates, type RebootState, type Updates } from "./types"

// How many of the host's latest changes the overview reads to find its last
// reboot and its last package change.
const CHANGES_READ = 50

// The short message of each apt failure code (UX-7 "failed change").
const APT_MESSAGES: Record<string, string> = {
  "system.apt_busy": "Another package manager is running",
  "system.repo_signature": "A package source failed its signature check",
  "system.disk_full": "The disk is full",
  "system.broken_packages": "Packages have broken dependencies",
  "system.dpkg_interrupted": "Package installation was interrupted",
  "system.apt_failed": "The package manager failed",
}

// Package changes a retry can plan again unchanged: their input is fixed.
const REFRESH: PendingChange = { action: "system.index.refresh", title: "Refresh package lists", input: {} }
const REPAIR: PendingChange = { action: "system.packages.repair", title: "Repair packages", input: {} }
const RETRYABLE: Partial<Record<string, PendingChange>> = { [REFRESH.action]: REFRESH, [REPAIR.action]: REPAIR }

function isPackageChange(action: string): boolean {
  return action.startsWith("system.index.") || action.startsWith("system.packages.")
}

function useSystemResource<T>(host: Host, name: string) {
  return useQuery({
    queryKey: ["resource", host.id, `system.${name}`],
    queryFn: () => readResource<T>(host.id, SYSTEM_MODULE, name),
    enabled: host.online,
  })
}

// FailedChange is the last package change when it failed: the code's short
// message, the failing package, and apt's last lines behind a tip.
function FailedChange({ change, disabled, onRetry }: { change: Change; disabled: boolean; onRetry: (p: PendingChange) => void }) {
  const error = change.error
  if (error === null) return null
  const [head, ...tail] = error.message.split("\n")
  const pkg = /^apt failed on (\S+):$/.exec(head ?? "")?.[1]
  const retry = RETRYABLE[change.action]
  return (
    <Alert variant={error.code === "system.apt_busy" ? "warning" : "destructive"} role="alert">
      <AlertTitle as="h4">{APT_MESSAGES[error.code] ?? "Last change failed"}</AlertTitle>
      <AlertDescription className="flex flex-col items-start gap-2">
        {pkg !== undefined && (
          <span className="text-sm">
            Package <span className="font-mono break-all">{pkg}</span>
          </span>
        )}
        <span className="flex flex-wrap items-center gap-2">
          <Link to={`/changes/${encodeURIComponent(change.id)}`} className="text-sm font-medium underline underline-offset-4">
            Open the change
          </Link>
          {tail.length > 0 && !disabled && (
            <InfoTip label="Package manager output">
              <pre className="font-mono text-xs whitespace-pre-wrap">{tail.join("\n")}</pre>
            </InfoTip>
          )}
          {retry !== undefined && (
            <Button size="sm" variant="outline" disabled={disabled} onClick={() => onRetry(retry)}>
              <RotateCcw aria-hidden />
              Retry
            </Button>
          )}
        </span>
      </AlertDescription>
    </Alert>
  )
}

function UpdatesTile({
  host,
  lastPackageChange,
  onChange,
}: {
  host: Host
  lastPackageChange: Change | undefined
  onChange: (p: PendingChange) => void
}) {
  const updates = useSystemResource<Updates>(host, "updates")
  const offline = !host.online
  const data = updates.data
  const upToDate = data !== undefined && data.upgradable.length === 0 && data.removals.length === 0
  const security = data?.upgradable.filter((u) => u.security).length ?? 0
  const failed = lastPackageChange !== undefined && lastPackageChange.state === "failed" ? lastPackageChange : undefined

  let status: { status: "ok" | "warn" | "error"; label: string } | undefined
  if (data?.dpkgInterrupted) status = { status: "error", label: "Interrupted" }
  else if (upToDate) status = { status: "ok", label: "Up to date" }
  else if (data !== undefined) status = { status: "warn", label: `${data.upgradable.length} upgradable` }

  return (
    <Tile
      icon={Download}
      title="Updates"
      greyed={offline}
      loading={updates.isPending && !offline}
      status={status}
      actions={
        <>
          <Button size="sm" variant="outline" disabled={offline} onClick={() => onChange(REFRESH)}>
            <RefreshCw aria-hidden />
            Refresh
          </Button>
          <Button
            size="sm"
            disabled={offline || data === undefined || upToDate}
            onClick={() => onChange({ action: "system.packages.upgrade", title: `Upgrade ${host.name}`, input: { all: true, names: [] } })}
          >
            Upgrade all
          </Button>
        </>
      }
    >
      {updates.isError && <TileError error={updates.error} onRetry={() => void updates.refetch()} disabled={offline} />}
      {updates.isPending && !offline && <TileSkeleton />}
      {data !== undefined && (
        <>
          {upToDate ? (
            <CircleCheck aria-hidden className="text-success-text size-8" />
          ) : (
            <div className="@container">
              <div className="grid grid-cols-1 gap-2 @min-[22rem]:grid-cols-2">
                <StatTile icon={Download} figure={data.upgradable.length} caption="Upgradable" />
                <StatTile icon={ShieldAlert} figure={security} caption="Security" />
              </div>
            </div>
          )}
          <span className="text-muted-foreground text-xs">{data.refreshedAt === null ? "Never refreshed" : `Refreshed ${formatTime(data.refreshedAt)}`}</span>
          {data.dpkgInterrupted && (
            <Alert variant="destructive" role="alert">
              <AlertTitle as="h4">Package installation was interrupted</AlertTitle>
              <AlertDescription>
                <Button size="sm" variant="outline" disabled={offline} onClick={() => onChange(REPAIR)}>
                  <Wrench aria-hidden />
                  Repair packages
                </Button>
              </AlertDescription>
            </Alert>
          )}
          {data.failedSources.length > 0 && (
            <Alert variant="warning">
              <AlertTitle as="h4">{`${data.failedSources.length} ${data.failedSources.length === 1 ? "source" : "sources"} failed at the last refresh`}</AlertTitle>
              <AlertDescription>
                <ul className="font-mono text-xs break-all">
                  {data.failedSources.map((source) => (
                    <li key={source}>{source}</li>
                  ))}
                </ul>
              </AlertDescription>
            </Alert>
          )}
        </>
      )}
      {failed !== undefined && <FailedChange change={failed} disabled={offline} onRetry={onChange} />}
    </Tile>
  )
}

function AutoUpdatesTile({ host, onChange }: { host: Host; onChange: (p: PendingChange) => void }) {
  const auto = useSystemResource<AutoUpdates>(host, "autoupdates")
  const offline = !host.online
  const data = auto.data
  const switchId = useId()
  let status: { status: "ok" | "off"; label: string } | undefined
  if (data !== undefined) status = !data.installed ? { status: "off", label: "Not installed" } : data.enabled ? { status: "ok", label: "On" } : { status: "off", label: "Off" }

  return (
    <Tile icon={CalendarClock} title="Automatic updates" greyed={offline} loading={auto.isPending && !offline} status={status}>
      {auto.isError && <TileError error={auto.error} onRetry={() => void auto.refetch()} disabled={offline} />}
      {auto.isPending && !offline && <TileSkeleton />}
      {data?.installed === true && (
        <span className="flex items-center gap-2 text-sm">
          <Switch
            id={switchId}
            checked={data.enabled}
            disabled={offline}
            onCheckedChange={(enabled) =>
              onChange({ action: "system.autoupdates.set", title: enabled ? "Turn on automatic updates" : "Turn off automatic updates", input: { enabled } })
            }
          />
          <label htmlFor={switchId}>Security updates</label>
        </span>
      )}
      {data?.installed === false && (
        <div>
          <Button
            size="sm"
            variant="outline"
            disabled={offline}
            onClick={() => onChange({ action: "system.packages.install", title: "Install unattended-upgrades", input: { names: ["unattended-upgrades"] } })}
          >
            Install unattended-upgrades
          </Button>
        </div>
      )}
    </Tile>
  )
}

function OverviewTiles({ host }: { host: Host }) {
  const [pending, setPending] = useState<PendingChange | null>(null)
  const reboot = useSystemResource<RebootState>(host, "reboot")
  const summary = useQuery({
    queryKey: ["resource", host.id, "hostinfo.summary"],
    queryFn: () => readResource<Summary>(host.id, "hostinfo", "summary"),
    enabled: host.online,
  })
  // Changes live on the controller, so they read while the host is offline:
  // the reboot state of a host that is rebooting comes from them.
  const changes = useQuery({
    queryKey: systemChangesKey(host.id),
    queryFn: () => request<{ changes: Change[] }>("GET", `/api/v1/changes?hostId=${encodeURIComponent(host.id)}&limit=${CHANGES_READ}`),
  })
  // A host that reconnects may have rebooted: read its boot time again.
  const { refetch: refetchSummary } = summary
  useEffect(() => {
    if (host.online) void refetchSummary()
  }, [host.online, refetchSummary])

  const list = changes.data?.changes ?? []
  const lastReboot = list.find((c) => c.action === "system.host.reboot")
  const lastPackageChange = list.find((c) => isPackageChange(c.action))

  return (
    <div className="flex flex-col gap-4">
      {!host.online && (
        <Alert variant="warning" role="status">
          <AlertTitle as="h3">Host offline</AlertTitle>
          <AlertDescription>{`Last seen ${formatTime(host.lastSeenAt)}`}</AlertDescription>
        </Alert>
      )}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <UpdatesTile host={host} lastPackageChange={lastPackageChange} onChange={setPending} />
        <RebootTile
          host={host}
          bootTime={summary.data?.bootTime}
          lastReboot={lastReboot}
          state={reboot.data}
          read={{ pending: reboot.isPending, error: reboot.error, retry: () => void reboot.refetch() }}
        />
        <AutoUpdatesTile host={host} onChange={setPending} />
        <DockerTile host={host} />
      </div>
      <SystemChange hostId={host.id} pending={pending} onClose={() => setPending(null)} />
    </div>
  )
}

// OverviewTab is the System module's overview (UI-5, UX-7): updates, reboot,
// automatic updates and Docker Engine tiles, each reading on its own so one
// failed read leaves the others usable.
export function OverviewTab({ host }: { host: Host }) {
  const state = host.modules.find((m) => m.id === SYSTEM_MODULE)
  if (state === undefined) {
    return (
      <EmptyState
        icon={<Server aria-hidden />}
        title="Agent update needed"
        description="This agent predates the system module."
        action={<UpdateNowButton host={host} label="Update agent" />}
      />
    )
  }
  if (!state.enabled) return <EmptyState title="Module not enabled" description="This module is not enabled on this host." />
  return <OverviewTiles host={host} />
}
