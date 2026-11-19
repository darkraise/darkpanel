import { Link, useSearchParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { Card, CardContent } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { ToggleGroup, ToggleGroupItem } from "darkraise-ui/components/toggle-group"
import { HOSTS_QUERY_KEY, errorMessage, listHosts } from "@/api/client"
import type { Host, ModuleState } from "@/api/types"
import { StatusPill } from "@/components/visual/status-pill"
import { formatTime } from "@/lib/format"
import { usePageTitle } from "@/lib/page-title"
import { CreateHostDialog } from "./create-host-dialog"
import { HostCard } from "./host-card"

type View = "cards" | "table"

const GRID = "grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4"

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

function LoadingCards() {
  return (
    <div role="status" aria-busy="true" aria-label="Loading the hosts" className={GRID}>
      {Array.from({ length: 4 }, (_, i) => (
        <Card key={i}>
          <CardContent className="flex flex-col gap-3 p-4">
            <Skeleton className="h-9 w-2/3" />
            <Skeleton className="h-5 w-1/2" />
            <Skeleton className="h-6 w-full" />
            <Skeleton className="h-6 w-full" />
          </CardContent>
        </Card>
      ))}
    </div>
  )
}

function HostsBody({ view }: { view: View }) {
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  if (hosts.isPending) return <LoadingCards />
  if (hosts.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
      </Alert>
    )
  }
  if (hosts.data.hosts.length === 0) {
    // darkraise-ui's EmptyState title is an h3, so a hidden h2 keeps the
    // heading levels under the page's h1 from skipping one.
    return (
      <>
        <h2 className="sr-only">Host list</h2>
        <EmptyState title="No hosts yet" />
      </>
    )
  }
  if (view === "cards") {
    return (
      <section aria-label="Host list" className={GRID}>
        {hosts.data.hosts.map((h) => (
          <HostCard key={h.id} host={h} />
        ))}
      </section>
    )
  }
  return (
    <div className="overflow-x-auto">
      {/* darkraise-ui lets table cells break anywhere, so without a minimum width
          a narrow table squeezes its words apart instead of scrolling. */}
      <Table className="min-w-[44rem]">
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>Platform</TableHead>
            <TableHead>Modules</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {hosts.data.hosts.map((h) => (
            <TableRow key={h.id}>
              <TableCell>
                <div className="flex flex-wrap items-center gap-2">
                  <Link to={`/hosts/${encodeURIComponent(h.id)}`} className="font-medium break-all underline-offset-4 hover:underline">
                    {h.name}
                  </Link>
                  {h.isLocal && (
                    <Badge variant="secondary" size="sm">
                      This controller
                    </Badge>
                  )}
                  {h.state === "pending" && (
                    <Badge variant="outline" size="sm">
                      Pending
                    </Badge>
                  )}
                  {h.state === "revoked" && (
                    <Badge variant="destructive" size="sm">
                      Revoked
                    </Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <div className="flex flex-col items-start gap-1">
                  <StatusPill status={h.online ? "ok" : "error"} label={h.online ? "Online" : "Offline"} />
                  <span className="text-xs text-muted-foreground">
                    {h.lastSeenAt === null ? "Never seen" : `Last seen ${formatTime(h.lastSeenAt)}`}
                  </span>
                  {h.ingestion !== null && !h.ingestion.ok && (
                    <>
                      <Badge variant="destructive" size="sm">
                        Journal ingestion failing
                      </Badge>
                      {h.ingestion.error !== null && (
                        <p className="text-xs text-muted-foreground">{h.ingestion.error}</p>
                      )}
                    </>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <div className="flex flex-col items-start gap-1">
                  <span className="break-all">{h.agentVersion ?? "—"}</span>
                  <span className="text-xs text-muted-foreground">
                    {h.protocolVersion === null ? "—" : `protocol ${h.protocolVersion}`}
                  </span>
                  {!h.supported && h.agentVersion !== null && (
                    <Badge variant="destructive" size="sm">
                      Unsupported
                    </Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>{h.os === null || h.arch === null ? "—" : `${h.os} / ${h.arch}`}</TableCell>
              <TableCell>
                <ModuleStates host={h} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

export function HostsPage() {
  usePageTitle("Hosts")
  const [params, setParams] = useSearchParams()
  const view: View = params.get("view") === "table" ? "table" : "cards"
  function choose(next: string) {
    if (next !== "cards" && next !== "table") return
    setParams(
      (prev) => {
        const out = new URLSearchParams(prev)
        if (next === "table") out.set("view", "table")
        else out.delete("view")
        return out
      },
      { replace: true },
    )
  }
  return (
    <>
      <PageHeader
        title="Hosts"
        description="The servers this controller manages."
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <ToggleGroup type="single" value={view} onValueChange={choose} aria-label="View">
              <ToggleGroupItem value="cards">Cards</ToggleGroupItem>
              <ToggleGroupItem value="table">Table</ToggleGroupItem>
            </ToggleGroup>
            <CreateHostDialog />
          </div>
        }
      />
      <HostsBody view={view} />
    </>
  )
}
