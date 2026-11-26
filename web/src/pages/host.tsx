import { Suspense } from "react"
import { useParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Server } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Breadcrumb, BreadcrumbItem, BreadcrumbLink, BreadcrumbList, BreadcrumbPage, BreadcrumbSeparator } from "darkraise-ui/components/breadcrumb"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, errorMessage, listHosts } from "@/api/client"
import type { Host } from "@/api/types"
import { SectionHeader } from "@/components/visual/section-header"
import { SkeletonTiles } from "@/components/visual/skeleton-tiles"
import { usePageTitle } from "@/lib/page-title"
import { HostScope, canOnHost, isAdmin } from "@/session/can"
import { useSession } from "@/session/use-session"
import { hostTabs, moduleRegistry, moduleView, type ModuleView } from "@/modules/registry"
import { ModuleOff } from "@/modules/module-off"
import { AdapterLink } from "@/router/adapter-link"
import { HostOverview } from "./host-overview"
import { hostStatus } from "./host-status"

function hostTitle(loaded: boolean, name: string | undefined, moduleId: string | undefined): string {
  if (!loaded) return "Host"
  if (name === undefined) return "Host not found"
  if (moduleId === undefined) return name
  const label = moduleRegistry.find((m) => m.id === moduleId)?.label ?? moduleId
  return `${name} · ${label}`
}

// darkraise-ui's EmptyState title is an h3, so a hidden h2 under the page's h1
// keeps the heading levels from skipping one.
function ModuleBody({ view, host }: { view: ModuleView; host: Host }) {
  switch (view.kind) {
    case "not-enabled":
      return (
        <>
          <h2 className="sr-only">Module</h2>
          <EmptyState data-testid="state-disabled" title="Module not enabled" description="This module is not enabled on this host." />
        </>
      )
    case "off":
      return <ModuleOff host={host} entry={view.entry} />
    case "unreported": {
      const Unreported = view.entry.unreported
      return (
        <>
          <h2 className="sr-only">{view.entry.label}</h2>
          <Suspense fallback={<Spinner aria-label="Loading the module" />}>
            <Unreported host={host} />
          </Suspense>
        </>
      )
    }
    case "unavailable": {
      const Unavailable = view.entry.unavailable
      if (Unavailable !== undefined) {
        return (
          <Suspense fallback={<Spinner aria-label="Loading the module" />}>
            <Unavailable host={host} reason={view.reason} />
          </Suspense>
        )
      }
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
          <ModulePages hostId={host.id} />
        </Suspense>
      )
    }
  }
}

// A module whose pages need more than the host's scope: only admin pages
// exist above operate, so this is the deep link the navigation never offers.
function OnlyAdmins() {
  return (
    <div className="dr-empty-state">
      <h2 className="dr-empty-state-title">Only admins can see this page.</h2>
    </div>
  )
}

type Tab = { label: string; value: string; href: string }

// The kit's PageHeader takes its title as a string, so the host page draws the
// same header parts itself to carry the icon and status pill beside the name.
function HostHeader({ host, tabs }: { host: Host; tabs: Tab[] }) {
  return (
    <div className="dr-page-header">
      <Breadcrumb>
        <BreadcrumbList>
          <BreadcrumbItem>
            <BreadcrumbLink asChild>
              <AdapterLink to="/">Hosts</AdapterLink>
            </BreadcrumbLink>
          </BreadcrumbItem>
          <BreadcrumbSeparator />
          <BreadcrumbItem>
            <BreadcrumbPage className="wrap-anywhere">{host.name}</BreadcrumbPage>
          </BreadcrumbItem>
        </BreadcrumbList>
      </Breadcrumb>
      <SectionHeader level={1} icon={Server} title={host.name} status={hostStatus(host)} />
      <nav aria-label="Page sections" className="dr-page-header-tabs overflow-x-auto">
        {tabs.map((tab) => (
          <AdapterLink key={tab.value} to={tab.href} className="dr-page-header-tab shrink-0 whitespace-nowrap" activeClassName="active">
            {tab.label}
          </AdapterLink>
        ))}
      </nav>
    </div>
  )
}

function HostLoading() {
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader level={1} icon={Server} title="Host" status={{ status: "off", label: "Loading" }} />
      <SkeletonTiles count={4} label="Loading the host" />
      <SkeletonTiles count={4} />
    </div>
  )
}

export function HostPage() {
  const { session } = useSession()
  const { hostId = "", moduleId } = useParams()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const host = hosts.data?.hosts.find((h) => h.id === hostId)
  usePageTitle(hostTitle(hosts.isSuccess, host?.name, moduleId))

  if (hosts.isPending) return <HostLoading />
  if (hosts.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(hosts.error)}</AlertDescription>
      </Alert>
    )
  }
  if (host === undefined) return <EmptyState title="Host not found" description={`No host has the id ${hostId}.`} />

  const base = `/hosts/${encodeURIComponent(host.id)}`
  const tabs: Tab[] = [
    { label: "Overview", value: "overview", href: base },
    ...hostTabs(moduleRegistry, host, session).map((m) => ({ label: m.label, value: m.id, href: `${base}/${m.id}` })),
  ]
  const header = <HostHeader host={host} tabs={tabs} />
  const scope = isAdmin(session) ? "admin" : host.scope
  const entry = moduleRegistry.find((m) => m.id === moduleId)

  let body
  if (moduleId === undefined) body = <HostOverview host={host} />
  else if (entry !== undefined && !canOnHost(host, entry.scope ?? "read", session)) body = <OnlyAdmins />
  else body = <ModuleBody view={moduleView(moduleRegistry, host, moduleId)} host={host} />
  return (
    <HostScope value={scope}>
      {header}
      {body}
    </HostScope>
  )
}
