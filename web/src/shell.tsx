import { useEffect, useState } from "react"
import { Outlet } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { History, KeyRound, Server } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { SidebarLayout, type NavGroup, type NavItem } from "darkraise-ui/layout"
import { HOSTS_QUERY_KEY, errorMessage, getListenerState, listHosts } from "@/api/client"
import { connectEvents } from "@/api/events"
import type { Host } from "@/api/types"
import { enabledModules, moduleRegistry } from "@/modules/registry"
import { useSession } from "@/session/use-session"

// navigation lists each host with its module pages under Hosts, and the
// pages that belong to no host in their own group. Until the host list has
// loaded, Hosts has no children; a failed load is said in the footer.
function navigation(hosts: Host[]): NavGroup[] {
  const hostItems: NavItem[] = hosts.map((host) => {
    const href = `/hosts/${encodeURIComponent(host.id)}`
    return {
      label: host.name,
      href,
      children: enabledModules(moduleRegistry, host).map((m) => ({ label: m.label, href: `${href}/${m.id}`, icon: m.icon })),
    }
  })
  const globals: NavItem[] = moduleRegistry.flatMap((entry) =>
    entry.global === undefined ? [] : [{ label: entry.global.label, href: `/${entry.global.path}`, icon: entry.icon }],
  )
  return [
    {
      items: [
        { label: "Hosts", href: "/", icon: Server, children: hostItems },
        { label: "Audit log", href: "/audit", icon: History },
      ],
    },
    ...(globals.length === 0 ? [] : [{ label: "Network", items: globals }]),
    { label: "Settings", items: [{ label: "API tokens", href: "/settings/tokens", icon: KeyRound }] },
  ]
}

// ListenerNotice says where the controller listens; without a Tailscale
// address it serves loopback only, and the footer says so (spec section 13).
function ListenerNotice() {
  const listeners = useQuery({ queryKey: ["listeners"], queryFn: getListenerState, refetchOnWindowFocus: true })
  if (listeners.isPending) return null
  if (listeners.isError) {
    return (
      <p role="status" className="px-3 py-2 text-xs text-destructive">
        {`Listener state unavailable: ${errorMessage(listeners.error)}`}
      </p>
    )
  }
  return (
    <p role="status" className={listeners.data.tailscaleBound ? "px-3 py-2 text-xs text-muted-foreground" : "px-3 py-2 text-xs text-warning"}>
      {listeners.data.message}
    </p>
  )
}

export function Shell() {
  const { session, logout } = useSession()
  const [logoutError, setLogoutError] = useState<string | null>(null)
  const client = useQueryClient()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  useEffect(() => connectEvents(client), [client])

  async function logOut() {
    setLogoutError(null)
    try {
      await logout()
    } catch (err) {
      setLogoutError(`Logout failed: ${errorMessage(err)}`)
    }
  }

  return (
    <SidebarLayout
      nav={navigation(hosts.data?.hosts ?? [])}
      showThemeSwitcher
      showLayoutSwitcher={false}
      notificationSlot={null}
      user={session === null ? undefined : { name: session.user.username, email: `Role: ${session.user.role}` }}
      onLogout={() => void logOut()}
      navFooter={
        <>
          {hosts.isError && (
            <p role="status" className="px-3 py-2 text-xs text-destructive">
              {`Host list unavailable: ${errorMessage(hosts.error)}`}
            </p>
          )}
          <ListenerNotice />
        </>
      }
    >
      {logoutError !== null && (
        <Alert variant="destructive" role="alert" className="mb-4">
          <AlertDescription>{logoutError}</AlertDescription>
        </Alert>
      )}
      <Outlet />
    </SidebarLayout>
  )
}
