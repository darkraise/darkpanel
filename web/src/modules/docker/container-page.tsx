import { Suspense } from "react"
import { Link, useParams } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, errorMessage, listHosts, readAction, readResource } from "@/api/client"
import type { ModuleState } from "@/api/types"
import { FIREWALL_MODULE, exposureQueryKey, type ExposureView } from "@/modules/firewall/types"
import { moduleRegistry } from "@/modules/registry"
import type { ContainerRef } from "@/modules/types"
import { LogsPanel } from "./logs-panel"
import { StatsPanel } from "./stats-panel"
import { DOCKER_ACTION_VERSION, type ContainerDetail, type DockerPort } from "./types"

type RefPort = ContainerRef["ports"][number]

function plainName(name: string): string {
  return name.replace(/^\//, "")
}

function refPorts(ports: DockerPort[]): RefPort[] {
  const groups = new Map<string, RefPort>()
  for (const p of ports) {
    if (!p.publicPort) continue
    const protocol = p.proto === "udp" ? "udp" : "tcp"
    const key = `${p.privatePort}/${protocol}`
    let group = groups.get(key)
    if (group === undefined) {
      group = { privatePort: p.privatePort, protocol, bindings: [] }
      groups.set(key, group)
    }
    group.bindings.push({ hostIp: p.ip === undefined || p.ip === "" ? "0.0.0.0" : p.ip, hostPort: p.publicPort })
  }
  return [...groups.values()]
}

function containerRef(hostId: string, detail: ContainerDetail): ContainerRef {
  return {
    hostId,
    containerId: detail.id,
    name: plainName(detail.name),
    ...(detail.service === undefined || detail.service === "" ? {} : { composeService: detail.service }),
    ports: refPorts(detail.ports),
  }
}

function BackLink() {
  return (
    <Link to="../.." relative="path" className="text-sm underline-offset-4 hover:underline">
      Back to Docker
    </Link>
  )
}

function Definition({ rows }: { rows: [string, string | undefined][] }) {
  return (
    <dl className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
      {rows.map(([label, value]) =>
        value === undefined || value === "" ? null : (
          <div key={label} className="contents">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="break-all">{value}</dd>
          </div>
        ),
      )}
    </dl>
  )
}

function ListSection({ title, items, empty }: { title: string; items: string[]; empty: string }) {
  return (
    <section aria-label={title} className="flex flex-col gap-1">
      <h3 className="font-medium">{title}</h3>
      {items.length === 0 ? (
        <p className="text-muted-foreground">{empty}</p>
      ) : (
        <ul className="font-mono text-sm">
          {items.map((text, i) => (
            <li key={i} className="break-all">
              {text}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function Inspect({ detail }: { detail: ContainerDetail }) {
  const state = detail.running ? "running" : `${detail.state} (exit code ${detail.exitCode})`
  return (
    <Card>
      <CardHeader>
        <CardTitle>Inspect</CardTitle>
      </CardHeader>
      <CardContent>
        <section aria-label="Inspect" className="flex flex-col gap-4">
          <Definition
            rows={[
              ["ID", detail.id],
              ["Image", detail.image],
              ["State", state],
              ["Restart policy", detail.restartPolicy],
              ["Compose project", detail.project],
              ["Compose service", detail.service],
              ["Owner", detail.owner],
            ]}
          />
          <ListSection title="Environment" items={detail.env.map((v) => `${v.name}=${v.value}`)} empty="No environment variables." />
          <ListSection
            title="Mounts"
            items={detail.mounts.map((m) => `${m.source} → ${m.destination}${m.rw ? "" : " (read-only)"}`)}
            empty="No mounts."
          />
          <ListSection
            title="Networks"
            items={detail.networks.map((n) => (n.ipv4 === undefined || n.ipv4 === "" ? n.network : `${n.network} ${n.ipv4}`))}
            empty="Not attached to any network."
          />
        </section>
      </CardContent>
    </Card>
  )
}

function Exposure({ hostId, containerRef: ref, modules, modulesPending }: { hostId: string; containerRef: ContainerRef; modules: ModuleState[]; modulesPending: boolean }) {
  const firewall = modules.find((s) => s.id === FIREWALL_MODULE)
  const usable = firewall !== undefined && firewall.enabled && firewall.available
  const exposure = useQuery({
    queryKey: exposureQueryKey(hostId),
    queryFn: () => readResource<ExposureView>(hostId, FIREWALL_MODULE, "exposure"),
    enabled: usable && ref.ports.length > 0,
  })

  let body
  if (modulesPending) {
    body = <Spinner aria-label="Loading the modules" />
  } else if (firewall === undefined || !firewall.enabled) {
    body = <p>The firewall module is not enabled on this host.</p>
  } else if (!firewall.available) {
    body = <p>{`The firewall module is unavailable: ${firewall.reason ?? "no reason was given"}.`}</p>
  } else if (ref.ports.length === 0) {
    body = <p>This container publishes no ports.</p>
  } else if (exposure.isPending) {
    body = <Spinner aria-label="Loading the exposure" />
  } else if (exposure.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(exposure.error)}</AlertDescription>
      </Alert>
    )
  } else {
    const rows = exposure.data.rows.filter(
      (row) => row.container?.name === ref.name && ref.ports.some((p) => p.protocol === row.proto && p.bindings.some((b) => b.hostPort === row.port)),
    )
    body =
      rows.length === 0 ? (
        <p>The firewall reports no listening port for this container.</p>
      ) : (
        <ul aria-label="Published ports" className="flex flex-col gap-2">
          {rows.map((row, i) => (
            <li key={i} className="flex flex-wrap items-center gap-2">
              <span className="font-mono">{`${row.proto} ${row.addr}:${row.port}`}</span>{" "}
              {row.classes.map((c, j) => (
                <Badge key={j} variant="outline" size="sm">{`${c.class}: ${c.verdict}`}</Badge>
              ))}
            </li>
          ))}
        </ul>
      )
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>Exposure</CardTitle>
      </CardHeader>
      <CardContent>{body}</CardContent>
    </Card>
  )
}

/** ContainerPage is the page of one container: what the daemon knows, its logs and statistics, its firewall exposure and the actions other modules offer for it. */
export function ContainerPage({ hostId }: { hostId: string }) {
  const { containerId = "" } = useParams()
  const queryClient = useQueryClient()
  const container = useQuery({
    queryKey: ["resource", hostId, "docker.container", containerId],
    queryFn: () => readAction<ContainerDetail>("docker.container.inspect", DOCKER_ACTION_VERSION, { container: containerId }, hostId),
  })
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const modules = hosts.data?.hosts.find((h) => h.id === hostId)?.modules ?? []

  if (container.isPending) return <Spinner aria-label="Loading the container" />
  if (container.isError) {
    return (
      <div className="flex flex-col gap-4">
        <BackLink />
        <Alert variant="destructive" role="alert">
          <AlertDescription>{errorMessage(container.error)}</AlertDescription>
        </Alert>
      </div>
    )
  }

  const detail = container.data
  const ref = containerRef(hostId, detail)
  const onDone = () => void queryClient.invalidateQueries({ queryKey: ["resource", hostId] })
  const actions = moduleRegistry
    .filter((entry) => modules.some((s) => s.id === entry.id && s.enabled))
    .flatMap((entry) => entry.containerActions ?? [])

  return (
    <div className="flex flex-col gap-6">
      <BackLink />
      <div className="flex items-center gap-3">
        <h2 className="text-xl font-semibold">{ref.name}</h2>
        <Badge variant={detail.running ? "default" : "secondary"}>{detail.running ? "running" : detail.state}</Badge>
      </div>
      <Inspect detail={detail} />
      <Card>
        <CardHeader>
          <CardTitle>Logs</CardTitle>
        </CardHeader>
        <CardContent>
          <LogsPanel hostId={hostId} containerId={detail.id} />
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Statistics</CardTitle>
        </CardHeader>
        <CardContent>
          <StatsPanel hostId={hostId} containerId={detail.id} />
        </CardContent>
      </Card>
      <Exposure hostId={hostId} containerRef={ref} modules={modules} modulesPending={hosts.isPending} />
      {actions.length === 0 ? null : (
        <Card>
          <CardHeader>
            <CardTitle>Actions</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            {actions.map((action) => (
              <section key={action.id} aria-label={action.label}>
                <Suspense fallback={<Spinner aria-label={"Loading " + action.label} />}>
                  <action.render container={ref} onDone={onDone} />
                </Suspense>
              </section>
            ))}
          </CardContent>
        </Card>
      )}
    </div>
  )
}
