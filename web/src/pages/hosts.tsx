import { Link, useSearchParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { RotateCcw, Server } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { ToggleGroup, ToggleGroupItem } from "darkraise-ui/components/toggle-group"
import { HOSTS_QUERY_KEY, errorMessage, listHosts } from "@/api/client"
import { InfoTip } from "@/components/visual/info-tip"
import { SectionHeader } from "@/components/visual/section-header"
import { SkeletonTiles } from "@/components/visual/skeleton-tiles"
import { StatusPill } from "@/components/visual/status-pill"
import { usePageTitle } from "@/lib/page-title"
import { CreateHostDialog } from "./create-host-dialog"
import { HostCard, MemoryCell, ModuleIcons } from "./host-card"
import { hostStatus } from "./host-status"

type View = "cards" | "table"

const GRID = "grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4"

function HostsBody({ view }: { view: View }) {
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  if (hosts.isPending) return <SkeletonTiles count={4} label="Loading the hosts" />
  if (hosts.isError) {
    return (
      <div className="flex flex-col items-start gap-2">
        <Alert variant="destructive" role="alert">
          <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
        </Alert>
        <Button size="sm" variant="outline" onClick={() => void hosts.refetch()}>
          <RotateCcw aria-hidden />
          Retry
        </Button>
      </div>
    )
  }
  if (hosts.data.hosts.length === 0) {
    // darkraise-ui's EmptyState title is an h3, so a hidden h2 keeps the
    // heading levels under the page's h1 from skipping one.
    return (
      <>
        <h2 className="sr-only">Host list</h2>
        <EmptyState icon={<Server aria-hidden />} title="No hosts yet" description="Add host enrolls the first server." />
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
            <TableHead>Host</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>System</TableHead>
            <TableHead>Memory</TableHead>
            <TableHead>Modules</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {hosts.data.hosts.map((h) => (
            <TableRow key={h.id}>
              <TableCell className="max-w-48">
                <div className="flex flex-col items-start gap-1">
                  <Link
                    to={`/hosts/${encodeURIComponent(h.id)}`}
                    title={h.name}
                    className="block max-w-full truncate font-medium underline-offset-4 hover:underline"
                  >
                    {h.name}
                  </Link>
                  {h.isLocal && (
                    <Badge variant="secondary" size="sm">
                      This controller
                    </Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <div className="flex flex-col items-start gap-1">
                  <StatusPill {...hostStatus(h)} />
                  {!h.supported && h.agentVersion !== null && <StatusPill status="error" label="Unsupported" />}
                  {h.ingestion !== null && !h.ingestion.ok && (
                    <span className="inline-flex items-center">
                      <StatusPill status="warn" label="Journal ingestion failing" />
                      {h.ingestion.error !== null && <InfoTip label="Why journal ingestion fails">{h.ingestion.error}</InfoTip>}
                    </span>
                  )}
                </div>
              </TableCell>
              <TableCell>{h.os === null || h.arch === null ? "—" : `${h.os} · ${h.arch}`}</TableCell>
              <TableCell className="min-w-40">
                <MemoryCell host={h} />
              </TableCell>
              <TableCell>
                <ModuleIcons host={h} />
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
      <SectionHeader
        level={1}
        icon={Server}
        title="Hosts"
        actions={
          <>
            <ToggleGroup type="single" value={view} onValueChange={choose} aria-label="View">
              <ToggleGroupItem value="cards">Cards</ToggleGroupItem>
              <ToggleGroupItem value="table">Table</ToggleGroupItem>
            </ToggleGroup>
            <CreateHostDialog />
          </>
        }
      />
      <HostsBody view={view} />
    </>
  )
}
