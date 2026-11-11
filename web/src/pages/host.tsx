import { Suspense } from "react"
import { useParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, errorMessage, listHosts } from "@/api/client"
import { usePageTitle } from "@/lib/page-title"
import { enabledModules, moduleRegistry, moduleView, type ModuleView } from "@/modules/registry"
import { HostOverview } from "./host-overview"

function hostTitle(loaded: boolean, name: string | undefined, moduleId: string | undefined): string {
  if (!loaded) return "Host"
  if (name === undefined) return "Host not found"
  if (moduleId === undefined) return name
  const label = moduleRegistry.find((m) => m.id === moduleId)?.label ?? moduleId
  return `${name} · ${label}`
}

// darkraise-ui's EmptyState title is an h3, so a hidden h2 under the page's h1
// keeps the heading levels from skipping one.
function ModuleBody({ view, hostId }: { view: ModuleView; hostId: string }) {
  switch (view.kind) {
    case "not-enabled":
      return (
        <>
          <h2 className="sr-only">Module</h2>
          <EmptyState title="Module not enabled" description="This module is not enabled on this host." />
        </>
      )
    case "unavailable": {
      const Icon = view.entry.icon
      return (
        <Alert variant="warning" role="status">
          <Icon aria-hidden="true" />
          <AlertTitle as="h2">{`${view.entry.label} is unavailable`}</AlertTitle>
          <AlertDescription>{view.reason}</AlertDescription>
        </Alert>
      )
    }
    case "pages": {
      const ModulePages = view.entry.routes
      return (
        <Suspense fallback={<Spinner aria-label="Loading the module" />}>
          <ModulePages hostId={hostId} />
        </Suspense>
      )
    }
  }
}

export function HostPage() {
  const { hostId = "", moduleId } = useParams()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const host = hosts.data?.hosts.find((h) => h.id === hostId)
  usePageTitle(hostTitle(hosts.isSuccess, host?.name, moduleId))

  if (hosts.isPending) return <Spinner aria-label="Loading the host" />
  if (hosts.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
      </Alert>
    )
  }
  if (host === undefined) return <EmptyState title="Host not found" description={`No host has the id ${hostId}.`} />

  const base = `/hosts/${encodeURIComponent(host.id)}`
  const tabs = [
    { label: "Overview", value: "overview", href: base },
    ...enabledModules(moduleRegistry, host).map((m) => ({ label: m.label, value: m.id, href: `${base}/${m.id}` })),
  ]
  const header = <PageHeader breadcrumbs={[{ label: "Hosts", href: "/" }, { label: host.name }]} title={host.name} tabs={tabs} />

  if (moduleId === undefined) {
    return (
      <>
        {header}
        <HostOverview host={host} />
      </>
    )
  }
  return (
    <>
      {header}
      <ModuleBody view={moduleView(moduleRegistry, host, moduleId)} hostId={host.id} />
    </>
  )
}
