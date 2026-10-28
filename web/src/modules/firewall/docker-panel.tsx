import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { DockerSources } from "./docker-sources"
import { FirewallChange } from "./firewall-change"
import { FIREWALL_MODULE, dockerQueryKey, type DockerIssue, type DockerNetwork, type DockerStatus } from "./types"

type BadgeVariant = "default" | "secondary" | "destructive" | "outline"

const HEALTH_BADGES: Record<DockerStatus["health"], BadgeVariant> = {
  ok: "default",
  degraded: "secondary",
  broken: "destructive",
  unsupported: "destructive",
  off: "outline",
}

const LEVEL_BADGES: Record<DockerIssue["level"], BadgeVariant> = {
  broken: "destructive",
  degraded: "secondary",
}

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

function orNone(value: string | undefined, none: string): string {
  return value === undefined || value === "" ? none : value
}

function protection(n: DockerNetwork): string {
  if (n.protected) return "Protected"
  return n.reason === undefined || n.reason === "" ? "Not protected" : `Not protected: ${n.reason}`
}

function Detection({ docker }: { docker: DockerStatus }) {
  const rows: [string, string][] = [
    ["Docker", orNone(docker.version, "unknown")],
    ["Firewall backend", orNone(docker.driver, "unknown")],
    ["Docker chains", orNone(docker.dockerVariant, "none found")],
    ["UFW chains", orNone(docker.ufwVariant, "none found")],
    ["Raw-table protection", docker.rawProtection ? "Loaded" : "Missing"],
    ["Supported", docker.supported ? "Yes" : "No"],
  ]
  return (
    <dl aria-label="Docker detection" className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-muted-foreground">{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function Networks({ networks }: { networks: DockerNetwork[] }) {
  return (
    <Table aria-label="Docker networks">
      <TableHeader>
        <TableRow>
          <TableHead>Network</TableHead>
          <TableHead>Bridge</TableHead>
          <TableHead>Subnets</TableHead>
          <TableHead>Protection</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {networks.map((n) => (
          <TableRow key={n.id}>
            <TableCell>{n.name}</TableCell>
            <TableCell className="font-mono">{n.bridge}</TableCell>
            <TableCell className="font-mono">{n.subnets.join(", ")}</TableCell>
            <TableCell>{protection(n)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function DockerBody({ hostId, docker }: { hostId: string; docker: DockerStatus }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const unsupported = docker.unsupported ?? []
  const issues = docker.issues ?? []
  const foreignRules = docker.foreignRules ?? []
  const manualRoutes = docker.manualRoutes ?? []
  const repairable = docker.enabled && (docker.health === "degraded" || docker.health === "broken")

  return (
    <>
      <Detection docker={docker} />
      {!docker.supported && (
        <Alert variant="warning" role="status">
          <AlertTitle>Unsupported Docker configuration</AlertTitle>
          <AlertDescription>
            <ul aria-label="Unsupported Docker reasons" className="mt-1 list-disc pl-5">
              {unsupported.map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {issues.length > 0 && (
        <ul aria-label="Docker health issues" className="flex flex-col gap-2">
          {issues.map((issue) => (
            <li key={`${issue.level}:${issue.reason}`} className="flex items-center gap-2">
              <Badge variant={LEVEL_BADGES[issue.level]} size="sm">{issue.level}</Badge>
              {issue.reason}
            </li>
          ))}
        </ul>
      )}
      {foreignRules.length > 0 && (
        <Alert variant="warning" role="status">
          <AlertTitle>Rules in DOCKER-USER that Darkpanel did not write</AlertTitle>
          <AlertDescription>
            <p>Darkpanel never discards them by itself.</p>
            <ul aria-label="Foreign DOCKER-USER rules" className="mt-1 list-disc pl-5 font-mono text-xs">
              {foreignRules.map((rule) => (
                <li key={rule}>{rule}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {manualRoutes.length > 0 && (
        <div className="flex flex-col gap-2">
          <p>These manual route rules have no effect on Docker traffic until a container rule binds a published port:</p>
          <ul aria-label="Manual route rules" className="list-disc pl-5 font-mono text-xs">
            {manualRoutes.map((rule) => (
              <li key={rule}>{rule}</li>
            ))}
          </ul>
        </div>
      )}
      {docker.networks.length > 0 && <Networks networks={docker.networks} />}
      <div className="flex flex-wrap gap-2">
        {!docker.enabled && (
          <Button
            disabled={!docker.supported}
            onClick={() => setPending({ action: "firewall.docker.enable", title: "Enable the Docker integration", input: {} })}
          >
            Enable integration
          </Button>
        )}
        {docker.enabled && (
          <Button
            variant="outline"
            onClick={() => setPending({ action: "firewall.docker.disable", title: "Disable the Docker integration", input: {} })}
          >
            Disable integration
          </Button>
        )}
        {repairable && (
          <Button onClick={() => setPending({ action: "firewall.docker.repair", title: "Repair the Docker integration", input: {} })}>Repair</Button>
        )}
        {docker.enabled && foreignRules.length > 0 && (
          <Button
            variant="outline"
            onClick={() =>
              setPending({
                action: "firewall.docker.repair",
                title: "Repair the Docker integration and discard foreign rules",
                input: { discardForeign: true },
              })
            }
          >
            Repair and discard foreign rules
          </Button>
        )}
      </div>
      <DockerSources hostId={hostId} docker={docker} />
      <FirewallChange
        hostId={hostId}
        action={pending?.action ?? "firewall.docker.enable"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </>
  )
}

// DockerPanel shows what the Docker integration found on the host and its
// health, and enables, disables or repairs it through the change dialog.
export function DockerPanel({ hostId }: { hostId: string }) {
  const query = useQuery({
    queryKey: dockerQueryKey(hostId),
    queryFn: () => readResource<DockerStatus>(hostId, FIREWALL_MODULE, "docker"),
  })
  if (query.isPending) return <Spinner aria-label="Loading the Docker integration" />
  if (query.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  }
  const docker = query.data
  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          Docker integration
          <Badge variant={HEALTH_BADGES[docker.health]} size="sm">{docker.health}</Badge>
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {docker.available ? (
          <DockerBody hostId={hostId} docker={docker} />
        ) : (
          <p>{orNone(docker.reason, "") === "" ? "Docker is not available on this host." : `Docker is not available on this host: ${docker.reason}`}</p>
        )}
      </CardContent>
    </Card>
  )
}
