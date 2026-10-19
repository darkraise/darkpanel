import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { HOSTS_QUERY_KEY, errorMessage, listHosts } from "@/api/client"
import type { Host, ModuleState } from "@/api/types"

function moduleStatus(m: ModuleState): { text: string; variant: BadgeVariant } {
  if (!m.enabled) return { text: "off", variant: "outline" }
  if (!m.available) return { text: "unavailable", variant: "destructive" }
  return { text: "on", variant: "default" }
}

function ModuleStates({ host }: { host: Host }) {
  const unavailable = host.modules.filter((m) => m.enabled && !m.available && m.reason !== null)
  return (
    <div className="flex flex-col gap-1">
      <div className="flex flex-wrap gap-1">
        {host.modules.map((m) => {
          const status = moduleStatus(m)
          return (
            <Badge key={m.id} variant={status.variant} size="sm">
              {`${m.id}: ${status.text}`}
            </Badge>
          )
        })}
      </div>
      {unavailable.map((m) => (
        <p key={m.id} className="text-xs text-muted-foreground">{`${m.id}: ${m.reason}`}</p>
      ))}
    </div>
  )
}

function HostsBody() {
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  if (hosts.isPending) return <Spinner aria-label="Loading the hosts" />
  if (hosts.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
      </Alert>
    )
  }
  if (hosts.data.hosts.length === 0) return <EmptyState title="No hosts yet" />
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Modules</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {hosts.data.hosts.map((h) => (
          <TableRow key={h.id}>
            <TableCell>
              <div className="flex items-center gap-2">
                <Link to={`/hosts/${encodeURIComponent(h.id)}`} className="font-medium underline-offset-4 hover:underline">
                  {h.name}
                </Link>
                {h.isLocal && (
                  <Badge variant="secondary" size="sm">
                    This controller
                  </Badge>
                )}
              </div>
            </TableCell>
            <TableCell>{h.online ? "Online" : "Offline"}</TableCell>
            <TableCell>
              <ModuleStates host={h} />
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function HostsPage() {
  return (
    <>
      <PageHeader title="Hosts" description="The servers this controller manages." />
      <HostsBody />
    </>
  )
}
