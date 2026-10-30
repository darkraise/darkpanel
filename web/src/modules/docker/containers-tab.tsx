import { useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { DockerChange } from "./docker-change"
import { DOCKER_MODULE, dockerKey, type DockerContainer, type DockerPort, type DockerUsage } from "./types"

interface Pending {
  action: string
  title: string
  container: string
}

const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"]

function bytes(n: number): string {
  let value = n
  let unit = 0
  while (Math.round(value * 10) / 10 >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit++
  }
  return `${Math.round(value * 10) / 10} ${UNITS[unit]}`
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
  return `${u.cpuPercent.toFixed(1)}% · ${bytes(u.memUsage)} / ${bytes(u.memLimit)}`
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
    <TableRow>
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
  const query = useQuery({
    queryKey: dockerKey(hostId, "containers"),
    queryFn: () => readResource<{ containers: DockerContainer[] }>(hostId, DOCKER_MODULE, "containers"),
  })

  if (query.isPending) return <Spinner aria-label="Loading the containers" />
  if (query.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  }
  if (query.data.containers.length === 0) return <EmptyState title="No containers" />
  return (
    <>
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
      {pending ? (
        <DockerChange hostId={hostId} action={pending.action} title={pending.title} input={{ container: pending.container }} onClose={() => setPending(null)} />
      ) : null}
    </>
  )
}
