import { useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Container, Download, ExternalLink, Server } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { changeError, changeInProgress, changeQueryKey, getChange, listSteps, readResource, stepsQueryKey } from "@/api/client"
import type { Change, Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { InfoTip } from "@/components/visual/info-tip"
import { SectionHeader } from "@/components/visual/section-header"
import { Stepper } from "@/components/visual/stepper"
import { StatusPill } from "@/components/visual/status-pill"
import { FIREWALL_MODULE, type FirewallStatus } from "@/modules/firewall/types"
import { UpdateNowButton } from "@/pages/update-now-dialog"
import { TileError } from "./tile"
import { SYSTEM_ACTION_VERSION, SYSTEM_MODULE, type DockerState } from "./types"

const INSTALL_ACTION = "system.docker.install"
// The firewall step's confirmation window (UX-5).
const CONFIRM_SECONDS = 900
const REPLACE_DOCS = "https://github.com/darkraise/darkpanel/blob/main/docs/user/modules/docker.md#replace-distro-docker"

// What the panel reads of an install change; a Change satisfies it.
export interface InstallChange {
  id?: string
  state: string
  error?: Change["error"]
}

function moduleOn(host: Host, id: string): boolean {
  return host.modules.some((m) => m.id === id && m.enabled && m.available)
}

// useFirewallFlag says whether the install turns on the firewall's Docker
// integration: the firewall module is enabled and available and UFW is
// active. Null until that is known.
function useFirewallFlag(host: Host) {
  const wanted = moduleOn(host, FIREWALL_MODULE)
  const status = useQuery({
    queryKey: ["resource", host.id, "firewall.status"],
    queryFn: () => readResource<FirewallStatus>(host.id, FIREWALL_MODULE, "status"),
    enabled: wanted && host.online,
  })
  if (!wanted) return { flag: false as boolean | null, status }
  return { flag: status.data === undefined ? null : status.data.active, status }
}

// InstallProgress follows an install the panel started: its composite steps
// while it runs, then where it ended.
function useInstall(started: InstallChange | null, lastInstall: InstallChange | undefined) {
  const id = started?.id
  const change = useQuery({ queryKey: changeQueryKey(id ?? ""), queryFn: () => getChange(id ?? ""), enabled: id !== undefined })
  const current: InstallChange | undefined = change.data ?? started ?? lastInstall
  const running = current !== undefined && changeInProgress(current as Change)
  const steps = useQuery({ queryKey: stepsQueryKey(id ?? ""), queryFn: () => listSteps(id ?? ""), enabled: id !== undefined && running })
  return { current, running, steps: steps.data }
}

function InstallStepper({ firewall, steps }: { firewall: boolean; steps: { action: string; state: string }[] | undefined }) {
  const all = [
    { id: "check", label: "Check" },
    { id: "engine", label: "Install Docker Engine" },
    ...(firewall ? [{ id: "firewall", label: "Firewall integration" }] : []),
  ]
  const done = steps?.filter((s) => s.state === "completed" || s.state === "confirmed").length ?? 0
  return <Stepper steps={all} current={all[Math.min(done, all.length - 1)]?.id ?? "check"} />
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
        icon={<Server aria-hidden />}
        title="Agent update needed"
        description="This agent predates the system module."
        action={<UpdateNowButton host={host} label="Update agent" />}
      />
    )
  }
  return <Panel host={host} reason={reason} lastInstall={lastInstall} />
}

function Panel({ host, reason, lastInstall }: { host: Host; reason: string; lastInstall?: InstallChange }) {
  const [open, setOpen] = useState(false)
  const [started, setStarted] = useState<InstallChange | null>(null)
  const docker = useQuery({
    queryKey: ["resource", host.id, "system.docker"],
    queryFn: () => readResource<DockerState>(host.id, SYSTEM_MODULE, "docker"),
    enabled: host.online,
  })
  const { flag, status: firewall } = useFirewallFlag(host)
  const install = useInstall(started, lastInstall)

  const header = <SectionHeader icon={Container} title="Docker Engine" />
  if (!host.online) {
    return (
      <div className="flex flex-col gap-4">
        {header}
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
        <SectionHeader icon={Container} title="Docker Engine" status={{ status: "warn", label: "Distribution package" }} />
        <Alert variant="warning">
          <AlertTitle as="h3">Replace the distribution Docker by hand first</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <ul className="font-mono text-sm break-all">
              {(d.conflicts ?? []).map((name) => (
                <li key={name}>{name}</li>
              ))}
            </ul>
            <a href={REPLACE_DOCS} target="_blank" rel="noreferrer" className="inline-flex items-center gap-1 text-sm font-medium underline underline-offset-4">
              How to replace it
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
        <SectionHeader
          icon={Container}
          title="Docker Engine"
          status={d.running ? { status: "ok", label: "Running" } : { status: "error", label: "Daemon stopped" }}
          actions={<InfoTip label="Why Docker is unavailable">{reason}</InfoTip>}
        />
        <span className="text-sm">
          docker-ce <span className="font-mono tabular-nums">{d.version ?? "—"}</span>
        </span>
      </div>
    )
  }

  const current = install.current
  const firewallPending = moduleOn(host, FIREWALL_MODULE) && flag === null
  return (
    <div className="flex flex-col gap-4">
      {header}
      {install.running && current !== undefined ? (
        <div className="flex flex-col gap-3">
          {current.state === "awaiting_confirmation" ? (
            <Alert variant="info" role="status">
              <AlertTitle as="h3">Confirm the firewall integration</AlertTitle>
              <AlertDescription>
                {current.id !== undefined && (
                  <Link to={`/changes/${encodeURIComponent(current.id)}`} className="text-sm font-medium underline underline-offset-4">
                    Keep or revert
                  </Link>
                )}
              </AlertDescription>
            </Alert>
          ) : (
            <StatusPill status="info" label="Installing" live />
          )}
          <InstallStepper firewall={flag === true} steps={install.steps} />
        </div>
      ) : (
        <div className="flex flex-col items-center gap-4 rounded-lg border border-dashed p-8 text-center">
          <span className="bg-muted text-muted-foreground flex size-16 items-center justify-center rounded-full">
            <Container aria-hidden className="size-8" />
          </span>
          <span className="font-medium">Docker Engine is not installed</span>
          {current?.state === "reverted" && (
            <Alert variant="warning" role="status">
              <AlertTitle as="h3">Install rolled back</AlertTitle>
            </Alert>
          )}
          {current !== undefined && current.state !== "reverted" && changeError(current as Change) !== null && (
            <Alert variant="destructive" role="alert">
              <AlertTitle as="h3">Install failed</AlertTitle>
              <AlertDescription className="break-words">{changeError(current as Change)}</AlertDescription>
            </Alert>
          )}
          {firewall.isError ? (
            <TileError error={firewall.error} onRetry={() => void firewall.refetch()} />
          ) : firewallPending ? (
            <Skeleton className="h-9 w-36" aria-busy />
          ) : (
            <>
              {flag === true && (
                <span className="text-muted-foreground flex items-center gap-1 text-sm">
                  With the firewall integration
                  <InfoTip label="About the firewall integration">
                    UFW keeps control of published ports. Confirm within 15 minutes, or the whole install rolls back.
                  </InfoTip>
                </span>
              )}
              <Button onClick={() => setOpen(true)}>
                <Download aria-hidden />
                Install Docker
              </Button>
            </>
          )}
        </div>
      )}
      <ChangeDialog
        open={open}
        onOpenChange={setOpen}
        title={`Install Docker on ${host.name}`}
        target={{ name: INSTALL_ACTION, version: SYSTEM_ACTION_VERSION, revertible: flag === true, revertSeconds: CONFIRM_SECONDS }}
        input={{ hostId: host.id, firewallIntegration: flag === true }}
        onApplied={(change) => setStarted(change)}
      />
    </div>
  )
}
