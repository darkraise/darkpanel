import { useEffect, useId, useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { CircleCheck, RefreshCw, RotateCcw, Server } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Switch } from "darkraise-ui/components/switch"
import { readResource, request } from "@/api/client"
import type { Change, Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import type { Status } from "@/components/visual/status"
import { formatTime } from "@/lib/format"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { UpdateNowButton } from "@/pages/update-now-dialog"
import { DockerTile } from "./docker-tile"
import { RebootTile } from "./reboot-tile"
import { SystemChange, type PendingChange } from "./system-change"
import { LastSeen, Tile, TileError, TileSkeleton } from "./tile"
import { SYSTEM_MODULE, systemChangesKey, HOST_OFFLINE, type AutoUpdates, type RebootState, type Updates } from "./types"

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
// Failures that leave the package database needing a repair (spec §4).
const REPAIRABLE = new Set(["system.dpkg_interrupted", "system.broken_packages"])

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

function retryTarget(host: Host, action: string): { change: PendingChange } | { to: string } | undefined {
  const fixed = RETRYABLE[action]
  if (fixed !== undefined) return { change: fixed }
  // A change record keeps no input, so a package set is chosen again where
  // it was chosen the first time.
  const base = `/hosts/${encodeURIComponent(host.id)}/system`
  if (action === "system.packages.upgrade") return { to: `${base}/updates` }
  if (action === "system.packages.install" || action === "system.packages.remove") return { to: `${base}/packages` }
  return undefined
}

function RetryButton({ host, change, disabled, onRetry }: { host: Host; change: Change; disabled: boolean; onRetry: (p: PendingChange) => void }) {
  const target = retryTarget(host, change.action)
  if (target === undefined) return null
  const label = (
    <>
      <RotateCcw aria-hidden />
      Retry
    </>
  )
  if ("change" in target) {
    return (
      <Button size="sm" variant="outline" disabled={disabled} onClick={() => onRetry(target.change)}>
        {label}
      </Button>
    )
  }
  if (disabled) {
    return (
      <Button size="sm" variant="outline" disabled>
        {label}
      </Button>
    )
  }
  return (
    <Button size="sm" variant="outline" asChild>
      <Link to={target.to}>{label}</Link>
    </Button>
  )
}

function ChangeLink({ change }: { change: Change }) {
  return (
    <Link to={`/changes/${encodeURIComponent(change.id)}`} className="text-sm font-medium underline underline-offset-4">
      Open the change
    </Link>
  )
}

// BusyAlert is a package change that found another package manager running:
// the updates tile keeps reading, and the change can be tried again.
function BusyAlert({ host, change, disabled, onRetry }: { host: Host; change: Change; disabled: boolean; onRetry: (p: PendingChange) => void }) {
  return (
    <Alert variant="warning" role="alert" data-testid="state-aptBusy">
      <AlertTitle as="h4">{APT_MESSAGES["system.apt_busy"]}</AlertTitle>
      <AlertDescription className="flex flex-wrap items-center gap-2">
        <RetryButton host={host} change={change} disabled={disabled} onRetry={onRetry} />
        <ChangeLink change={change} />
      </AlertDescription>
    </Alert>
  )
}

// LastChangeTile is the last package change when it failed: the code's short
// message, the failing package, and apt's last lines behind a tip.
function LastChangeTile({ host, change, onRetry }: { host: Host; change: Change; onRetry: (p: PendingChange) => void }) {
  const error = change.error
  if (error === null) return null
  const offline = !host.online
  const [head, ...tail] = error.message.split("\n")
  const pkg = /^apt failed on (\S+):$/.exec(head ?? "")?.[1]
  return (
    <Tile title="Last change" status={{ status: "error", label: "Failed" }} greyed={offline}>
      <div className="flex flex-col items-start gap-2" data-testid="state-failedChange">
        <span className="flex flex-wrap items-center gap-1 text-sm">
          {APT_MESSAGES[error.code] ?? "Last change failed"}
          {tail.length > 0 && !offline && (
            <InfoTip label="apt output">
              <pre className="font-mono text-xs whitespace-pre-wrap">{tail.join("\n")}</pre>
            </InfoTip>
          )}
        </span>
        {pkg !== undefined && (
          <span className="text-sm">
            Package <span className="font-mono break-all">{pkg}</span>
          </span>
        )}
        <span className="flex flex-wrap items-center gap-2">
          {REPAIRABLE.has(error.code) ? (
            <Button size="sm" variant="outline" disabled={offline} onClick={() => onRetry(REPAIR)}>
              Repair packages
            </Button>
          ) : (
            <RetryButton host={host} change={change} disabled={offline} onRetry={onRetry} />
          )}
          <ChangeLink change={change} />
        </span>
      </div>
    </Tile>
  )
}

function Figure({ value, caption }: { value: number; caption: string }) {
  return (
    <span className="flex items-baseline gap-1">
      <span className="font-mono text-2xl font-semibold tabular-nums">{value}</span>
      <span className="text-muted-foreground text-sm">{caption}</span>
    </span>
  )
}

function UpdatesTile({
  host,
  busy,
  onChange,
}: {
  host: Host
  busy: Change | undefined
  onChange: (p: PendingChange) => void
}) {
  const updates = useSystemResource<Updates>(host, "updates")
  const offline = !host.online
  const data = updates.data
  // An interrupted dpkg leaves the lists unread, which is not "up to date".
  const upToDate = data !== undefined && !data.dpkgInterrupted && data.upgradable.length === 0 && data.removals.length === 0
  const security = data?.upgradable.filter((u) => u.security).length ?? 0
  // Nor are its counts read: the tile offers the repair alone.
  const interrupted = data?.dpkgInterrupted === true

  let status: { status: Status; label: string } | undefined
  if (offline) status = HOST_OFFLINE
  else if (data?.dpkgInterrupted) status = { status: "error", label: "Interrupted" }
  else if (busy !== undefined) status = { status: "warn", label: "Busy" }
  else if (upToDate) status = { status: "ok", label: "Up to date" }
  else if (data !== undefined) status = { status: "warn", label: `${data.upgradable.length} upgradable` }

  const upgradeAll = (
    <Button
      size="sm"
      disabled={offline || data === undefined}
      onClick={() => onChange({ action: "system.packages.upgrade", title: `Upgrade ${host.name}`, input: { all: true, names: [] } })}
    >
      Upgrade all
    </Button>
  )
  const actions = offline ? (
    upgradeAll
  ) : (
    <>
      <Button size="sm" variant="outline" disabled={updates.isPending} onClick={() => onChange(REFRESH)}>
        <RefreshCw aria-hidden />
        Refresh
      </Button>
      {!upToDate && !interrupted && upgradeAll}
    </>
  )

  return (
    <Tile title="Updates" greyed={offline} loading={updates.isPending && !offline} status={status} actions={actions}>
      {offline && <LastSeen host={host} testId="state-offline" />}
      {!offline && updates.isError && <TileError error={updates.error} onRetry={() => void updates.refetch()} />}
      {!offline && updates.isPending && <TileSkeleton />}
      {!offline && data !== undefined && (
        <>
          {interrupted ? null : upToDate ? (
            <CircleCheck role="img" aria-label="Up to date" className="text-success-text size-8" data-testid="state-upToDate" />
          ) : (
            <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
              <Figure value={data.upgradable.length} caption="pending" />
              <Figure value={security} caption="security" />
            </div>
          )}
          <span className="text-muted-foreground text-xs">{data.refreshedAt === null ? "Never refreshed" : `Refreshed ${formatTime(data.refreshedAt)}`}</span>
          {data.dpkgInterrupted && (
            <Alert variant="destructive" role="alert" data-testid="state-dpkgInterrupted">
              <AlertTitle as="h4">Package installation was interrupted</AlertTitle>
              <AlertDescription className="flex flex-col items-start gap-2">
                <span>The updates cannot be read until the packages are repaired.</span>
                <Button size="sm" variant="outline" onClick={() => onChange(REPAIR)}>
                  Repair packages
                </Button>
              </AlertDescription>
            </Alert>
          )}
          {data.failedSources.length > 0 && (
            <Alert variant="warning" data-testid="state-failedSources">
              <AlertTitle as="h4">{`${data.failedSources.length} ${data.failedSources.length === 1 ? "source" : "sources"} failed at the last refresh`}</AlertTitle>
              <AlertDescription className="font-mono text-xs break-all">{data.failedSources.join(", ")}</AlertDescription>
            </Alert>
          )}
        </>
      )}
      {!offline && busy !== undefined && <BusyAlert host={host} change={busy} disabled={offline} onRetry={onChange} />}
    </Tile>
  )
}

function AutoUpdatesTile({ host, onChange }: { host: Host; onChange: (p: PendingChange) => void }) {
  const auto = useSystemResource<AutoUpdates>(host, "autoupdates")
  const offline = !host.online
  const data = auto.data
  const switchId = useId()
  let status: { status: Status; label: string } | undefined
  if (offline) status = HOST_OFFLINE
  else if (data !== undefined) status = !data.installed ? { status: "off", label: "Not installed" } : data.enabled ? { status: "ok", label: "On" } : { status: "off", label: "Off" }

  return (
    <Tile title="Automatic updates" greyed={offline} loading={auto.isPending && !offline} status={status}>
      {offline && <LastSeen host={host} />}
      {!offline && auto.isError && <TileError error={auto.error} onRetry={() => void auto.refetch()} />}
      {!offline && auto.isPending && <TileSkeleton />}
      {!offline && data?.installed === true && (
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
      {!offline && data?.installed === false && (
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
  const failed = lastPackageChange?.state === "failed" ? lastPackageChange : undefined
  const busy = failed?.error?.code === "system.apt_busy" ? failed : undefined

  // The changes give the last package change and the reboot in progress,
  // the summary the boot time a reboot is judged by: without them those
  // states cannot show, so say so rather than show nothing.
  const unread = [changes.isError && "recent changes", host.online && summary.isError && "boot time"].filter(Boolean).join(" and ")

  return (
    <div className="flex flex-col gap-4">
      {unread !== "" && (
        <Alert variant="warning" role="status" data-testid="state-partialRead">
          <AlertTitle as="h3">{`The host's ${unread} could not be read`}</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <span>The last change and a reboot in progress may not show.</span>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                if (changes.isError) void changes.refetch()
                if (summary.isError) void refetchSummary()
              }}
            >
              <RotateCcw aria-hidden />
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      )}
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <UpdatesTile host={host} busy={busy} onChange={setPending} />
        <RebootTile
          host={host}
          bootTime={summary.data?.bootTime}
          lastReboot={lastReboot}
          state={reboot.data}
          read={{ pending: reboot.isPending, error: reboot.error, retry: () => void reboot.refetch() }}
        />
        <AutoUpdatesTile host={host} onChange={setPending} />
        <DockerTile host={host} />
        {failed !== undefined && busy === undefined && <LastChangeTile host={host} change={failed} onRetry={setPending} />}
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
        data-testid="state-agentTooOld"
        icon={<Server aria-hidden />}
        title="Agent update needed"
        description="This agent predates the system module."
        action={<UpdateNowButton host={host} label="Update agent" />}
      />
    )
  }
  if (!state.enabled) return <EmptyState data-testid="state-disabled" title="Module not enabled" description="This module is not enabled on this host." />
  return <OverviewTiles host={host} />
}
