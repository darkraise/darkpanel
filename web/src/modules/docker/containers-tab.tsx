import { useLayoutEffect, useRef, useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { formatBytes } from "@/lib/format"
import { DockerChange } from "./docker-change"
import { DOCKER_MODULE, dockerKey, type DockerContainer, type DockerPort, type DockerUsage } from "./types"

interface Pending {
  action: string
  title: string
  container: string
}

// The request whose dialog has closed, kept until the read that the close
// started has landed, with the query's dataUpdatedAt at the close.
interface Closed {
  pending: Pending
  updatedAt: number
}

function portText(p: DockerPort): string {
  const target = `${p.privatePort}/${p.proto}`
  return p.publicPort ? `${p.publicPort}→${target}` : target
}

function ports(list: DockerPort[]): string {
  const texts = [...new Set(list.map(portText))]
  return texts.length === 0 ? "—" : texts.join(", ")
}

function project(c: DockerContainer): string {
  if (!c.project) return "—"
  return c.service ? `${c.project} / ${c.service}` : c.project
}

function usage(u: DockerUsage | undefined): string {
  if (!u) return "—"
  return `${u.cpuPercent.toFixed(1)}% · ${formatBytes(u.memUsage)} / ${formatBytes(u.memLimit)}`
}

function ContainerRow({ container: c, onChange }: { container: DockerContainer; onChange: (p: Pending) => void }) {
  const running = c.state === "running"
  const owned = c.owner !== undefined && c.owner !== ""
  const act = (verb: string, action: string) => {
    const label = `${verb} ${c.name}`
    return (
      <Button
        size="sm"
        variant="outline"
        aria-label={label}
        disabled={owned || (verb === "Remove" && running)}
        onClick={() => onChange({ action, title: label, container: c.id })}
      >
        {verb}
      </Button>
    )
  }
  return (
    <TableRow data-container={c.id}>
      <TableCell>
        <Link to={`containers/${encodeURIComponent(c.id)}`} className="underline-offset-4 hover:underline">
          {c.name}
        </Link>
        {owned ? (
          <Badge variant="secondary" size="sm" className="ml-2">
            owned by {c.owner}
          </Badge>
        ) : null}
      </TableCell>
      <TableCell className="font-mono">{c.image}</TableCell>
      <TableCell>
        <Badge variant={running ? "default" : "secondary"} size="sm" title={c.status}>
          {c.state}
        </Badge>
      </TableCell>
      <TableCell>{ports(c.ports)}</TableCell>
      <TableCell>{project(c)}</TableCell>
      <TableCell>{usage(c.usage)}</TableCell>
      <TableCell>
        <div className="flex gap-2">
          {running ? (
            <>
              {act("Stop", "docker.container.stop")}
              {act("Restart", "docker.container.restart")}
            </>
          ) : (
            act("Start", "docker.container.start")
          )}
          {act("Remove", "docker.container.remove")}
        </div>
      </TableCell>
    </TableRow>
  )
}

// ContainersTab lists the host's Docker containers and starts, stops,
// restarts or removes one through the change dialog.
export function ContainersTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const group = useRef<HTMLDivElement>(null)
  const closed = useRef<Closed | null>(null)
  const query = useQuery({
    queryKey: dockerKey(hostId, "containers"),
    queryFn: () => readResource<{ containers: DockerContainer[] }>(hostId, DOCKER_MODULE, "containers"),
  })

  // The dialog gives focus back to its trigger, but the read that follows a
  // change can remove that trigger (a stopped container has no Stop, a
  // removed one has no row) and drop focus to the page body. A layout effect
  // moves it before the browser paints a frame without focus. Only focus
  // that fell to the body moves: a read that lands late, after a failed one,
  // must not pull focus from where the user has since put it.
  useLayoutEffect(() => {
    const last = closed.current
    const root = group.current
    if (last === null || last.updatedAt === query.dataUpdatedAt) return
    closed.current = null
    if (root === null || (document.activeElement !== null && document.activeElement !== document.body)) return
    const row = Array.from(root.querySelectorAll<HTMLElement>("[data-container]")).find((r) => r.dataset.container === last.pending.container)
    const actions = Array.from(row?.querySelectorAll<HTMLButtonElement>("button") ?? []).filter((b) => !b.disabled)
    const target = actions.find((b) => b.getAttribute("aria-label") === last.pending.title) ?? actions[0] ?? root
    target.focus()
  }, [query.dataUpdatedAt])

  if (query.isPending) return <Spinner aria-label="Loading the containers" />
  if (query.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <div ref={group} role="group" aria-label="Containers" tabIndex={-1}>
      {query.data.containers.length === 0 ? (
        <EmptyState title="No containers" />
      ) : (
        <Table aria-label="Docker containers">
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Image</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Ports</TableHead>
              <TableHead>Project</TableHead>
              <TableHead>Usage</TableHead>
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {query.data.containers.map((c) => (
              <ContainerRow key={c.id} container={c} onChange={setPending} />
            ))}
          </TableBody>
        </Table>
      )}
      {pending ? (
        <DockerChange
          hostId={hostId}
          action={pending.action}
          title={pending.title}
          input={{ container: pending.container }}
          onClose={() => {
            closed.current = { pending, updatedAt: query.dataUpdatedAt }
            setPending(null)
          }}
        />
      ) : null}
    </div>
  )
}
