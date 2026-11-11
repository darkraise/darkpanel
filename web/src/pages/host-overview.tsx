import { useId, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readAction } from "@/api/client"
import type { Host, HostState, UpdateStatus } from "@/api/types"
import { stateVariant } from "@/lib/badges"
import { formatTime } from "@/lib/format"

const STATE_LABEL: Record<HostState, string> = { pending: "Pending", enrolled: "Enrolled", revoked: "Revoked" }

function OverviewCard({ title, children }: { title: string; children: ReactNode }) {
  const id = useId()
  return (
    <section aria-labelledby={id}>
      <Card>
        <CardHeader>
          <CardTitle>
            <h2 id={id}>{title}</h2>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">{children}</CardContent>
      </Card>
    </section>
  )
}

function hostStatus(host: Host): string {
  const online = host.online ? "Online" : "Offline"
  return host.lastSeenAt === null ? `${online}, never seen` : `${online}, last seen ${formatTime(host.lastSeenAt)}`
}

function HostFacts({ host }: { host: Host }) {
  const rows: [string, string][] = [
    ["State", STATE_LABEL[host.state]],
    ["Status", hostStatus(host)],
    ["Agent version", host.agentVersion ?? "—"],
    ["Protocol version", host.protocolVersion === null ? "—" : String(host.protocolVersion)],
    ["OS", host.os ?? "—"],
    ["Architecture", host.arch ?? "—"],
    ["Supported", host.supported ? "Yes" : "No"],
  ]
  return (
    <dl className="grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-[max-content_1fr]">
      {rows.map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="break-all">{value}</dd>
        </div>
      ))}
    </dl>
  )
}

function UpdateHistory({ hostId }: { hostId: string }) {
  const status = useQuery({
    queryKey: ["update-status", hostId],
    queryFn: () => readAction<UpdateStatus>("core.update.status", 1, {}, hostId),
  })
  if (status.isPending) return <Spinner aria-label="Loading the update history" />
  if (status.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(status.error)}</AlertDescription>
      </Alert>
    )
  }
  if (status.data.transactions.length === 0) return <EmptyState title="No updates yet" />
  return (
    <div className="overflow-x-auto">
      <Table aria-label="Update history">
        <TableHeader>
          <TableRow>
            <TableHead>Started</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Error</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {status.data.transactions.map((t) => (
            <TableRow key={t.id}>
              <TableCell>{formatTime(t.startedAt)}</TableCell>
              <TableCell>{t.kind}</TableCell>
              <TableCell className="break-all">{`${t.fromVersion} → ${t.toVersion}`}</TableCell>
              <TableCell>
                <Badge variant={stateVariant(t.state)} size="sm">
                  {t.state}
                </Badge>
              </TableCell>
              <TableCell>{t.error?.message ?? ""}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

export function HostOverview({ host }: { host: Host }) {
  return (
    <div className="flex flex-col gap-4">
      <OverviewCard title="Host">
        <HostFacts host={host} />
      </OverviewCard>
      <OverviewCard title="Updates">
        <div className="flex flex-wrap gap-2"></div>
        {host.online ? (
          <UpdateHistory hostId={host.id} />
        ) : (
          <p className="text-sm text-muted-foreground">The update history is unavailable while the host is offline.</p>
        )}
      </OverviewCard>
      <OverviewCard title="Enrollment and removal">
        <div className="flex flex-wrap gap-2"></div>
      </OverviewCard>
    </div>
  )
}
