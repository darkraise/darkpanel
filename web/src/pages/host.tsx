import { Suspense } from "react"
import { Navigate, useParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, errorMessage, listHosts } from "@/api/client"
import { enabledModules, moduleRegistry, moduleView, type ModuleView } from "@/modules/registry"

function ModuleBody({ view, hostId }: { view: ModuleView; hostId: string }) {
  switch (view.kind) {
    case "not-enabled":
      return <EmptyState title="Module not enabled" description="This module is not enabled on this host." />
    case "unavailable": {
      const Icon = view.entry.icon
      return (
        <Alert variant="warning" role="status">
          <Icon aria-hidden="true" />
          <AlertTitle>{`${view.entry.label} is unavailable`}</AlertTitle>
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

  if (hosts.isPending) return <Spinner aria-label="Loading the host" />
  if (hosts.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
      </Alert>
    )
  }
  const host = hosts.data.hosts.find((h) => h.id === hostId)
  if (host === undefined) return <EmptyState title="Host not found" description={`No host has the id ${hostId}.`} />

  const tabs = enabledModules(moduleRegistry, host).map((m) => ({
    label: m.label,
    value: m.id,
    href: `/hosts/${encodeURIComponent(host.id)}/${m.id}`,
  }))
  const header = <PageHeader breadcrumbs={[{ label: "Hosts", href: "/" }, { label: host.name }]} title={host.name} tabs={tabs} />

  if (moduleId === undefined) {
    const first = tabs[0]
    if (first !== undefined) return <Navigate to={first.href} replace />
    return (
      <>
        {header}
        <EmptyState title="No modules" description="No modules are enabled on this host." />
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
