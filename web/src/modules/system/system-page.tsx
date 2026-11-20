import { useNavigate } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Server } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { HOSTS_QUERY_KEY, errorMessage, listHosts, readResource } from "@/api/client"
import type { Host } from "@/api/types"
import { SectionHeader } from "@/components/visual/section-header"
import { OverviewTab } from "./overview-tab"
import { PackagesTab } from "./packages-tab"
import { UpdatesTab } from "./updates-tab"
import { SYSTEM_MODULE, type RebootState } from "./types"

// SystemHeader names the page and, once the reboot read has answered,
// whether the host needs a reboot. It shares the reboot tile's query.
function SystemHeader({ host }: { host: Host }) {
  const reboot = useQuery({
    queryKey: ["resource", host.id, "system.reboot"],
    queryFn: () => readResource<RebootState>(host.id, SYSTEM_MODULE, "reboot"),
    enabled: host.online,
  })
  const status = reboot.data?.required ? { status: "warn" as const, label: "Reboot needed" } : undefined
  return <SectionHeader icon={Server} title="System" status={status} />
}

export type SystemTab = "overview" | "updates" | "packages"

// SystemPage is /hosts/:hostId/system: the overview, the upgradable
// packages and the installed packages, one tab each at its own address.
export function SystemPage({ hostId, tab = "overview" }: { hostId: string; tab?: SystemTab }) {
  const navigate = useNavigate()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const host = hosts.data?.hosts.find((h) => h.id === hostId)
  if (hosts.isPending) return <Spinner aria-label="Loading the host" />
  if (hosts.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
      </Alert>
    )
  }
  if (host === undefined) return <EmptyState title="Host not found" description={`No host has the id ${hostId}.`} />
  return (
    <div className="flex flex-col gap-4">
      <SystemHeader host={host} />
      <Tabs
        value={tab}
        onValueChange={(next) => {
          const base = `/hosts/${encodeURIComponent(host.id)}/system`
          void navigate(next === "overview" ? base : `${base}/${next}`)
        }}
      >
        <TabsList aria-label="System">
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="updates">Updates</TabsTrigger>
          <TabsTrigger value="packages">Packages</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <OverviewTab host={host} />
        </TabsContent>
        <TabsContent value="updates">
          <UpdatesTab host={host} />
        </TabsContent>
        <TabsContent value="packages">
          <PackagesTab host={host} />
        </TabsContent>
      </Tabs>
    </div>
  )
}
