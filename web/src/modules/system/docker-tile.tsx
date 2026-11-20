import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Container, Download } from "lucide-react"
import { Button } from "darkraise-ui/components/button"
import { readResource } from "@/api/client"
import type { Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { StatusPill } from "@/components/visual/status-pill"
import { FIREWALL_MODULE, dockerQueryKey, statusQueryKey, type DockerStatus, type FirewallStatus } from "@/modules/firewall/types"
import { DockerUninstall } from "./docker-uninstall"
import { Tile, TileError, TileSkeleton } from "./tile"
import { SYSTEM_ACTION_VERSION, SYSTEM_MODULE, type DockerState } from "./types"

type Open = null | "install" | "update"

function dockerStatus(d: DockerState): { status: "ok" | "warn" | "error" | "off"; label: string } {
  if (d.state === "absent") return { status: "off", label: "Not installed" }
  if (d.state === "distro") return { status: "warn", label: "Distribution package" }
  return d.running ? { status: "ok", label: "Running" } : { status: "error", label: "Stopped" }
}

// DockerTile is the System overview's Docker Engine tile: its state and
// version, and the install (the same change as the Docker page's install
// panel), update and uninstall flows (UX-5, UX-6).
export function DockerTile({ host }: { host: Host }) {
  const [open, setOpen] = useState<Open>(null)
  const [uninstalling, setUninstalling] = useState(false)
  const offline = !host.online
  const firewallOn = host.modules.some((m) => m.id === FIREWALL_MODULE && m.enabled && m.available)
  const docker = useQuery({
    queryKey: ["resource", host.id, "system.docker"],
    queryFn: () => readResource<DockerState>(host.id, SYSTEM_MODULE, "docker"),
    enabled: host.online,
  })
  const ufw = useQuery({
    queryKey: statusQueryKey(host.id),
    queryFn: () => readResource<FirewallStatus>(host.id, FIREWALL_MODULE, "status"),
    enabled: host.online && firewallOn,
  })
  const integration = useQuery({
    queryKey: dockerQueryKey(host.id),
    queryFn: () => readResource<DockerStatus>(host.id, FIREWALL_MODULE, "docker"),
    enabled: host.online && firewallOn && docker.data?.state === "docker-ce",
  })
  const flag = firewallOn && ufw.data?.active === true
  const d = docker.data

  let actions = null
  if (d?.state === "absent") {
    actions = (
      <Button size="sm" disabled={offline || (firewallOn && ufw.isPending)} onClick={() => setOpen("install")}>
        <Download aria-hidden />
        Install Docker
      </Button>
    )
  } else if (d?.state === "docker-ce") {
    actions = (
      <>
        <Button size="sm" disabled={offline || d.updateAvailable !== true} onClick={() => setOpen("update")}>
          Update
        </Button>
        <Button size="sm" variant="outline" disabled={offline || (firewallOn && integration.isPending)} onClick={() => setUninstalling((u) => !u)}>
          {uninstalling ? "Cancel" : "Uninstall"}
        </Button>
      </>
    )
  }

  return (
    <Tile icon={Container} title="Docker Engine" greyed={offline} loading={docker.isPending && !offline} status={d === undefined ? undefined : dockerStatus(d)} actions={actions}>
      {docker.isError && <TileError error={docker.error} onRetry={() => void docker.refetch()} disabled={offline} />}
      {docker.isPending && !offline && <TileSkeleton />}
      {d?.state === "docker-ce" && (
        <>
          <span className="text-sm">
            docker-ce <span className="font-mono tabular-nums">{d.version ?? "—"}</span>
          </span>
          {d.updateAvailable && (
            <div>
              <StatusPill status="info" label="Update available" />
            </div>
          )}
          {uninstalling && !offline && <DockerUninstall host={host} docker={d} integrationOn={integration.data?.enabled === true} />}
        </>
      )}
      {d?.state === "distro" && (
        <ul className="font-mono text-xs break-all">
          {(d.conflicts ?? []).map((name) => (
            <li key={name}>{name}</li>
          ))}
        </ul>
      )}
      <ChangeDialog
        open={open === "install"}
        onOpenChange={(next) => setOpen(next ? "install" : null)}
        title={`Install Docker on ${host.name}`}
        target={{ name: "system.docker.install", version: SYSTEM_ACTION_VERSION, revertible: flag, revertSeconds: 900, confirmLabel: "Keep" }}
        input={{ hostId: host.id, firewallIntegration: flag }}
      />
      <ChangeDialog
        open={open === "update"}
        onOpenChange={(next) => setOpen(next ? "update" : null)}
        title={`Update Docker on ${host.name}`}
        target={{ name: "system.docker.engine-update", version: SYSTEM_ACTION_VERSION, hostId: host.id, module: SYSTEM_MODULE }}
        input={{}}
      />
    </Tile>
  )
}
