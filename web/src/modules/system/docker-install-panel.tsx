import { useEffect, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Container, Download, ExternalLink, RotateCcw, Server } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { HOSTS_QUERY_KEY, changeError, changeInProgress, changeQueryKey, getChange, listSteps, readResource, stepsQueryKey } from "@/api/client"
import type { Change, Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { InfoTip } from "@/components/visual/info-tip"
import { SectionHeader } from "@/components/visual/section-header"
import { Stepper } from "@/components/visual/stepper"
import { StatusPill } from "@/components/visual/status-pill"
import type { Status } from "@/components/visual/status"
import { FIREWALL_MODULE, statusQueryKey, type FirewallStatus } from "@/modules/firewall/types"
import { UpdateNowButton } from "@/pages/update-now-dialog"
import { Confirmation } from "./install-confirmation"
import { INSTALL_ACTION, useLatestInstall } from "./install-lookup"
import { installSteps, lastDeadline, stepAt } from "./install-steps"
import { TileError } from "./tile"
import { SYSTEM_ACTION_VERSION, SYSTEM_MODULE, type DockerState } from "./types"

// The firewall step's confirmation window (UX-5).
const CONFIRM_SECONDS = 900
const REPLACE_DOCS = "https://github.com/darkraise/darkpanel/blob/main/docs/user/modules/docker.md#replace-distro-docker"

// What the panel reads of an install change; a Change satisfies it.
export interface InstallChange {
  id?: string
  state: string
  error?: Change["error"]
}

const SUCCEEDED = new Set(["completed", "confirmed"])

function moduleOn(host: Host, id: string): boolean {
  return host.modules.some((m) => m.id === id && m.enabled && m.available)
}

// useFirewallFlag says whether the install turns on the firewall's Docker
// integration: the firewall module is enabled and available and UFW is
// active. Null until that is known.
function useFirewallFlag(host: Host) {
  const wanted = moduleOn(host, FIREWALL_MODULE)
  const status = useQuery({
    queryKey: statusQueryKey(host.id),
    queryFn: () => readResource<FirewallStatus>(host.id, FIREWALL_MODULE, "status"),
    enabled: wanted && host.online,
  })
  if (!wanted) return { flag: false as boolean | null, status }
  return { flag: status.data === undefined ? null : status.data.active, status }
}

// useInstall follows the install the panel started or, after a reload, the
// host's latest one that is still running or ended badly within a day: its
// composite steps, then where it ended.
function useInstall(host: Host, started: InstallChange | null, lastInstall: InstallChange | undefined) {
  const lookup = useLatestInstall(host.id, host.online && started === null && lastInstall === undefined)
  const found = started === null ? (lookup.data ?? undefined) : undefined
  const id = started?.id ?? found?.id
  const change = useQuery({ queryKey: changeQueryKey(id ?? ""), queryFn: () => getChange(id ?? ""), enabled: id !== undefined })
  const current: InstallChange | undefined = change.data ?? started ?? lastInstall ?? found
  const running = current !== undefined && changeInProgress(current as Change)
  const steps = useQuery({ queryKey: stepsQueryKey(id ?? ""), queryFn: () => listSteps(id ?? ""), enabled: id !== undefined })
  const failedRead = change.isError ? change : steps.isError ? steps : undefined
  return { current, running, steps: steps.data, failedRead, lookup }
}

function Header({ status }: { status?: { status: Status; label: string } }) {
  return <SectionHeader icon={Container} title="Docker" status={status} />
}

// DockerInstallPanel is the Docker page of a host where Docker Engine is
// not usable (UI-6, UX-5, UX-8): it offers the install where Docker is
// absent, names a distribution Docker to replace by hand, or shows a
// stopped daemon.
export function DockerInstallPanel({ host, reason, lastInstall }: { host: Host; reason: string; lastInstall?: InstallChange }) {
  const system = host.modules.find((m) => m.id === SYSTEM_MODULE)
  if (system === undefined) {
    return (
      <EmptyState
        data-testid="state-agentTooOld"
        icon={<Server aria-hidden />}
        title="Agent update needed"
        description="This agent cannot install Docker."
        action={<UpdateNowButton host={host} label="Update agent" />}
      />
    )
  }
  return <Panel host={host} reason={reason} lastInstall={lastInstall} />
}

function Panel({ host, reason, lastInstall }: { host: Host; reason: string; lastInstall?: InstallChange }) {
  const client = useQueryClient()
  const [open, setOpen] = useState(false)
  const [started, setStarted] = useState<InstallChange | null>(null)
  // Whether the operator pressed Revert here, rather than letting the window lapse.
  const [revertedHere, setRevertedHere] = useState(false)
  const docker = useQuery({
    queryKey: ["resource", host.id, "system.docker"],
    queryFn: () => readResource<DockerState>(host.id, SYSTEM_MODULE, "docker"),
    enabled: host.online,
  })
  const { flag, status: firewall } = useFirewallFlag(host)
  const install = useInstall(host, started, lastInstall)
  const succeeded = install.current !== undefined && SUCCEEDED.has(install.current.state)
  // A finished install changes the docker read and the host's module
  // availability; read both again rather than show "not installed".
  // Not while the dialog is open: the module turning available swaps this
  // panel, and the dialog with it, for the Docker pages.
  useEffect(() => {
    if (!succeeded || open) return
    void client.invalidateQueries({ queryKey: ["resource", host.id] })
    void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
  }, [succeeded, open, client, host.id])

  if (!host.online) {
    return (
      <div className="flex flex-col gap-4">
        <Header />
        <EmptyState title="Host offline" description="Docker Engine reads again once the host is back." />
      </div>
    )
  }
  if (docker.isError) return <TileError error={docker.error} onRetry={() => void docker.refetch()} />
  if (docker.data === undefined) return <Skeleton className="h-40 w-full" aria-busy />
  const d = docker.data

  if (d.state === "distro") {
    return (
      <div className="flex flex-col gap-4">
        <Header status={{ status: "warn", label: "Distribution package" }} />
        <Alert variant="warning" data-testid="state-dockerDistro">
          <AlertTitle as="h3">Replace the distribution packages by hand first</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <span className="flex flex-wrap gap-2">
              {(d.conflicts ?? []).map((name) => (
                <StatusPill key={name} status="warn" label={name} />
              ))}
            </span>
            <a href={REPLACE_DOCS} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm font-medium underline underline-offset-4">
              How to replace them
              <ExternalLink aria-hidden className="size-3.5" />
            </a>
          </AlertDescription>
        </Alert>
      </div>
    )
  }

  if (d.state === "docker-ce") {
    return (
      <div className="flex flex-col gap-4">
        <Header status={d.running ? { status: "ok", label: "Running" } : { status: "error", label: "Daemon stopped" }} />
        <span className="flex items-center gap-1 text-sm" data-testid={d.running ? undefined : "state-dockerStopped"}>
          {d.running ? (
            <>
              docker-ce <span className="font-mono tabular-nums">{d.version ?? "—"}</span>
            </>
          ) : (
            <StatusPill status="error" label="Daemon stopped" />
          )}
          <InfoTip label="Why Docker is unavailable">{reason}</InfoTip>
        </span>
      </div>
    )
  }

  const current = install.current
  const firewallPending = moduleOn(host, FIREWALL_MODULE) && flag === null
  const all = installSteps(flag === true)
  // "center" sits in the empty state, "start" under an alert, "inline" in one.
  const offerInstall = (label: string, icon = <Download aria-hidden />, layout: "center" | "start" | "inline" = "center") =>
    firewall.isError ? (
      <TileError error={firewall.error} onRetry={() => void firewall.refetch()} />
    ) : firewallPending ? (
      <Skeleton className="h-9 w-36" aria-busy />
    ) : (
      <div className={layout === "center" ? "flex justify-center" : "flex"}>
        <Button size={layout === "inline" ? "sm" : undefined} variant={layout === "inline" ? "outline" : undefined} onClick={() => setOpen(true)}>
          {icon}
          {label}
        </Button>
      </div>
    )

  let body
  if (install.failedRead !== undefined) {
    const failed = install.failedRead
    body = (
      <>
        <Header status={{ status: "warn", label: "Install state unknown" }} />
        <TileError error={failed.error} onRetry={() => void failed.refetch()} />
      </>
    )
  } else if (install.running && current !== undefined) {
    const awaiting = current.state === "awaiting_confirmation"
    body = (
      <>
        <Header status={awaiting ? { status: "info", label: "Awaiting confirmation" } : { status: "info", label: "Installing" }} />
        <div className="flex flex-col gap-3" data-testid={awaiting ? "state-awaitingConfirm" : "state-installing"}>
          <Stepper steps={all} current={stepAt(all, install.steps, awaiting)} />
          {awaiting && current.id !== undefined ? (
            <Confirmation
              changeId={current.id}
              deadline={lastDeadline(install.steps)}
              onDecided={(kept) => {
                setRevertedHere(!kept)
                void client.invalidateQueries({ queryKey: ["resource", host.id] })
                void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
              }}
            />
          ) : (
            <div>
              <StatusPill status="info" label="Installing docker-ce" live />
            </div>
          )}
        </div>
      </>
    )
  } else if (current !== undefined && current.state !== "reverted" && changeError(current as Change) !== null) {
    const failedAt = stepAt(all, install.steps, false, true)
    body = (
      <>
        <Header status={{ status: "error", label: "Install failed" }} />
        <Stepper steps={all} current={failedAt} failed />
        <Alert variant="destructive" role="alert" data-testid="state-installFailed">
          <AlertTitle as="h3">{failedAt === "firewall" ? "The firewall refused the Docker integration" : "Install failed"}</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <span className="break-words">{changeError(current as Change)}</span>
            <span>The install was rolled back.</span>
            {offerInstall("Retry", <RotateCcw aria-hidden />, "inline")}
          </AlertDescription>
        </Alert>
      </>
    )
  } else if (succeeded && d.state === "absent") {
    body = (
      <>
        <Header status={{ status: "info", label: "Installed" }} />
        <div data-testid="state-installFinishing">
          <StatusPill status="info" label="Finishing install" live />
        </div>
      </>
    )
  } else if (current?.state === "reverted") {
    body = (
      <>
        <Header status={{ status: "off", label: "Not installed" }} />
        <Alert variant="warning" role="status" data-testid="state-rolledBack">
          <AlertTitle as="h3">Install rolled back</AlertTitle>
          <AlertDescription>{revertedHere ? "You reverted the firewall step." : "The firewall step was not confirmed in time."}</AlertDescription>
        </Alert>
        {offerInstall("Install Docker", undefined, "start")}
      </>
    )
  } else {
    body = (
      <>
        <Header status={{ status: "off", label: "Not installed" }} />
        <EmptyState
          data-testid="state-dockerAbsent"
          icon={<Container aria-hidden />}
          title="Docker is not installed"
          description="Install Docker Engine from Docker's apt repository."
          action={offerInstall("Install Docker")}
        />
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {install.lookup.isError && <TileError error={install.lookup.error} onRetry={() => void install.lookup.refetch()} />}
      {body}
      <ChangeDialog
        open={open}
        onOpenChange={setOpen}
        title={`Install Docker on ${host.name}`}
        target={{ name: INSTALL_ACTION, version: SYSTEM_ACTION_VERSION, revertible: flag === true, revertSeconds: CONFIRM_SECONDS, confirmLabel: "Keep" }}
        input={{ hostId: host.id, firewallIntegration: flag === true }}
        onApplied={(change) => {
          setRevertedHere(false)
          setStarted(change)
        }}
      />
    </div>
  )
}
