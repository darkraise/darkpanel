import { useId, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Link } from "react-router"
import { Trash2 } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { HOSTS_QUERY_KEY } from "@/api/client"
import type { Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { InfoTip } from "@/components/visual/info-tip"
import { FIREWALL_ACTION_VERSION, FIREWALL_MODULE } from "@/modules/firewall/types"
import { SYSTEM_ACTION_VERSION, SYSTEM_MODULE, type DockerState } from "./types"

type Open = null | "firewall" | "uninstall"

function counts(d: DockerState): string {
  const n = (k: number | undefined, one: string, many: string) => `${k ?? 0} ${(k ?? 0) === 1 ? one : many}`
  return `${n(d.containers, "container", "containers")}, ${n(d.images, "image", "images")}, ${n(d.volumes, "volume", "volumes")}`
}

// DockerUninstall removes Docker Engine (UX-6): the firewall integration
// is turned off first, Darkpanel-managed containers are named with links to
// their pages, and the purge box is off by default. The plan names a
// pending firewall change when the backend refuses for one.
export function DockerUninstall({ host, docker, integrationOn }: { host: Host; docker: DockerState; integrationOn: boolean }) {
  const [open, setOpen] = useState<Open>(null)
  const [purge, setPurge] = useState(false)
  const purgeId = useId()
  const blockedId = useId()
  const client = useQueryClient()
  // Docker's state, the firewall's integration and the host's modules all
  // change with an uninstall. They are read again when the dialog closes:
  // read on apply, Docker's absence would unmount this section, and the
  // dialog with it, before it shows the change.
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ["resource", host.id] })
    void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
  }
  const labelled = docker.labelled ?? []
  const daemonUp = docker.running === true
  // A purge needs the daemon's counts: a choice made while it ran lapses when it stops.
  const purging = purge && daemonUp

  if (integrationOn) {
    return (
      <div className="flex flex-col gap-2">
        <Alert variant="warning">
          <AlertTitle as="h4">Turn off the firewall integration first</AlertTitle>
          <AlertDescription>Published ports stay reachable until Docker is removed</AlertDescription>
        </Alert>
        <div>
          <Button size="sm" variant="outline" onClick={() => setOpen("firewall")}>
            Turn off firewall integration
          </Button>
        </div>
        <ChangeDialog
          open={open === "firewall"}
          onOpenChange={(next) => setOpen(next ? "firewall" : null)}
          title="Turn off the firewall integration"
          target={{ name: "firewall.docker.disable", version: FIREWALL_ACTION_VERSION, hostId: host.id, module: FIREWALL_MODULE }}
          input={{}}
        />
      </div>
    )
  }

  return (
    <div className="flex flex-col gap-2">
      {labelled.length > 0 && (
        <Alert variant="destructive">
          <AlertTitle as="h4">Darkpanel manages these containers</AlertTitle>
          <AlertDescription id={blockedId}>
            <p className="mb-1">Remove them on their pages first; Docker cannot be uninstalled while they exist.</p>
            <ul className="flex flex-col gap-1">
              {labelled.map((c) => (
                <li key={`${c.module}/${c.name}`}>
                  <Link to={`/hosts/${encodeURIComponent(host.id)}/${encodeURIComponent(c.module)}`} className="font-mono text-sm break-all underline underline-offset-4">
                    {c.name}
                  </Link>
                </li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      <span className="flex items-center gap-2 text-sm">
        <input
          id={purgeId}
          type="checkbox"
          className="accent-primary size-4"
          checked={purging}
          disabled={!daemonUp}
          onChange={(e) => setPurge(e.target.checked)}
        />
        <label htmlFor={purgeId}>Delete all containers, images and volumes</label>
        {!daemonUp && <InfoTip label="Why purge is off">The Docker daemon does not answer, so nothing can be counted or deleted.</InfoTip>}
      </span>
      {purging && <span className="text-muted-foreground text-xs tabular-nums">{counts(docker)}</span>}
      <div>
        <Button
          size="sm"
          variant="outline"
          disabled={labelled.length > 0}
          aria-describedby={labelled.length > 0 ? blockedId : undefined}
          onClick={() => setOpen("uninstall")}
        >
          <Trash2 aria-hidden />
          Uninstall
        </Button>
      </div>
      <ChangeDialog
        open={open === "uninstall"}
        onOpenChange={(next) => {
          setOpen(next ? "uninstall" : null)
          if (!next) refresh()
        }}
        title={purging ? `Uninstall Docker and delete its data on ${host.name}` : `Uninstall Docker on ${host.name}`}
        target={{ name: "system.docker.uninstall", version: SYSTEM_ACTION_VERSION, hostId: host.id, module: SYSTEM_MODULE }}
        input={{ purge: purging }}
      />
    </div>
  )
}
