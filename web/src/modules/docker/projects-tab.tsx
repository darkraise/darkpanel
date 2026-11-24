import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { CirclePlay, CircleStop, Download, RotateCw } from "lucide-react"
import { errorMessage, readResource } from "@/api/client"
import { Fact } from "@/components/visual/fact"
import type { Status } from "@/components/visual/status"
import { StatusPill } from "@/components/visual/status-pill"
import { formatTime } from "@/lib/format"
import { DockerChange } from "./docker-change"
import { DOCKER_MODULE, dockerKey, type DockerProject } from "./types"
import { Can } from "@/session/can"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

const VERBS = [
  { verb: "Up", icon: CirclePlay, action: "docker.compose.up", title: (name: string) => `Start project ${name}` },
  { verb: "Down", icon: CircleStop, action: "docker.compose.down", title: (name: string) => `Take down project ${name}` },
  { verb: "Pull", icon: Download, action: "docker.compose.pull", title: (name: string) => `Pull images of project ${name}` },
  { verb: "Restart", icon: RotateCw, action: "docker.compose.restart", title: (name: string) => `Restart project ${name}` },
]

function listOrNone(items: string[]): string {
  return items.length === 0 ? "none" : items.join(", ")
}

function runningStatus(p: DockerProject): Status {
  if (p.containers.length === 0 || p.running === 0) return "off"
  return p.running === p.containers.length ? "ok" : "warn"
}

function parseProfiles(typed: string): string[] {
  return typed.split(/[,\s]+/).filter((p) => p !== "")
}

function sameList(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((entry, i) => entry === b[i])
}

function ProjectCard({ project, onChange }: { project: DockerProject; onChange: (pending: Pending) => void }) {
  const [typed, setTyped] = useState(project.profiles.join(", "))
  const profiles = parseProfiles(typed)
  const edited = !sameList(profiles, project.profiles)
  const rows: [string, string][] = [
    ["Directory", project.directory],
    ["Compose files", listOrNone(project.files)],
    ["Env files", listOrNone(project.envFiles)],
    ["Containers", listOrNone(project.containers)],
    ["Last run", project.lastRun === undefined ? "never" : formatTime(project.lastRun)],
  ]
  const profilesId = `project-${project.name}-profiles`

  return (
    <section aria-label={`Project ${project.name}`}>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            {project.name}
            <StatusPill status={runningStatus(project)} label={`${project.running} of ${project.containers.length} running`} />
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">
          {project.hashChanged && (
            <Alert variant="warning" role="status">
              <AlertDescription>The Compose files changed since the last run.</AlertDescription>
            </Alert>
          )}
          {project.configHash === "" && (
            <Alert variant="warning" role="status">
              <AlertDescription>The Compose files cannot be read.</AlertDescription>
            </Alert>
          )}
          <div role="group" aria-label="Project details" className="flex flex-wrap gap-1">
            {rows.map(([label, value]) => (
              <Fact key={label} label={label} value={value} mono={label !== "Last run"} />
            ))}
          </div>
          <Can need="admin">
          <div className="flex flex-col gap-2">
            <Label htmlFor={profilesId}>Profiles</Label>
            <Input id={profilesId} value={typed} placeholder="none" autoComplete="off" onChange={(e) => setTyped(e.target.value)} />
          </div>
          <div className="flex flex-wrap gap-2">
            {VERBS.map((v) => (
              <Button
                key={v.verb}
                variant="outline"
                aria-label={`${v.verb} ${project.name}`}
                onClick={() =>
                  onChange({
                    action: v.action,
                    title: v.title(project.name),
                    input: edited ? { project: project.name, profiles } : { project: project.name },
                  })
                }
              >
                <v.icon aria-hidden />
                {v.verb}
              </Button>
            ))}
          </div></Can>
        </CardContent>
      </Card>
    </section>
  )
}

// ProjectsTab lists the Compose projects found on the host and starts, takes
// down, pulls or restarts one through the change dialog.
export function ProjectsTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const query = useQuery({
    queryKey: dockerKey(hostId, "projects"),
    queryFn: () => readResource<{ projects: DockerProject[] }>(hostId, DOCKER_MODULE, "projects"),
  })

  let content
  if (query.isPending) {
    content = <Spinner aria-label="Loading projects" />
  } else if (query.isError) {
    content = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  } else if (query.data.projects.length === 0) {
    content = <EmptyState title="No Compose projects" />
  } else {
    content = (
      <ul aria-label="Compose projects" className="flex flex-col gap-4">
        {query.data.projects.map((p) => (
          <li key={p.name}>
            <ProjectCard project={p} onChange={setPending} />
          </li>
        ))}
      </ul>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      {content}
      <DockerChange
        hostId={hostId}
        action={pending?.action ?? "docker.compose.up"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </div>
  )
}
