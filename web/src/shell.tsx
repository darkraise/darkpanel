import { useEffect, useState } from "react"
import { Outlet } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Bell, BellRing, Boxes, History, KeyRound, Laptop, LogIn, RefreshCw, Server, Users } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { SidebarLayout, type NavGroup, type NavItem } from "darkraise-ui/layout"
import { HOSTS_QUERY_KEY, errorMessage, getListenerState, listHosts } from "@/api/client"
import { connectEvents } from "@/api/events"
import type { Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import { StatusPill } from "@/components/visual/status-pill"
import type { Status } from "@/components/visual/status"
import { AdminExposureBanner } from "@/modules/npm/banner"
import { PanelExposureBanner } from "@/modules/panel/banner"
import { hostTabs, moduleRegistry } from "@/modules/registry"
import { isAdmin } from "@/session/can"
import { useSession } from "@/session/use-session"

// navigation lists each host with its module pages under Hosts, and the
// pages that belong to no host in their own group. Until the host list has
// loaded, Hosts has no children; a failed load is said in the footer. The
// Network pages and the admin settings are left out for anyone but an admin,
// and a module page out of the host's scope is left out of its host.
function navigation(hosts: Host[], admin: boolean): NavGroup[] {
  const hostItems: NavItem[] = hosts.map((host) => {
    const href = `/hosts/${encodeURIComponent(host.id)}`
    return {
      label: host.name,
      href,
      children: hostTabs(moduleRegistry, host).map((m) => ({ label: m.label, href: `${href}/${m.id}`, icon: m.icon })),
    }
  })
  const globals: NavItem[] = !admin
    ? []
    : moduleRegistry.flatMap((entry) =>
        entry.global === undefined ? [] : [{ label: entry.global.label, href: `/${entry.global.path}`, icon: entry.icon }],
      )
  const settings: NavItem[] = [
    ...(admin
      ? [
          { label: "Users", href: "/settings/users", icon: Users },
          { label: "Host groups", href: "/settings/groups", icon: Boxes },
          { label: "Sign-in", href: "/settings/sign-in", icon: LogIn },
          { label: "Notifications", href: "/settings/notifications", icon: Bell },
        ]
      : []),
    { label: "API tokens", href: "/settings/tokens", icon: KeyRound },
    { label: "Tailscale devices", href: "/settings/devices", icon: Laptop },
  ]
  return [
    {
      items: [
        { label: "Hosts", href: "/", icon: Server, children: hostItems },
        { label: "Alerts", href: "/alerts", icon: BellRing },
        { label: "Audit log", href: "/audit", icon: History },
        { label: "Rollouts", href: "/rollouts", icon: RefreshCw },
      ],
    },
    ...(globals.length === 0 ? [] : [{ label: "Network", items: globals }]),
    { label: "Settings", items: settings },
  ]
}

// ListenerNotice says where the controller listens; without a Tailscale
// address it serves loopback only, and the footer says so (spec section 13).
// The chip carries the state; the controller's own message sits in the tip.
function ListenerNotice() {
  const listeners = useQuery({ queryKey: ["listeners"], queryFn: getListenerState, refetchOnWindowFocus: true })
  if (listeners.isPending) return null
  let chip: { status: Status; label: string }
  let detail: string
  if (listeners.isError) {
    chip = { status: "error", label: "Listener state unavailable" }
    detail = errorMessage(listeners.error)
  } else {
    chip = listeners.data.tailscaleBound ? { status: "ok", label: "Tailscale" } : { status: "warn", label: "Loopback only" }
    detail = listeners.data.message
  }
  return (
    <div className="flex items-center gap-1 px-3 py-2">
      <StatusPill live status={chip.status} label={chip.label} />
      <InfoTip label="Listener details">{detail}</InfoTip>
    </div>
  )
}

export function Shell() {
  const { session, logout } = useSession()
  const [logoutError, setLogoutError] = useState<string | null>(null)
  const client = useQueryClient()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const admin = isAdmin(session)
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
      nav={navigation(hosts.data?.hosts ?? [], admin)}
      showThemeSwitcher
      showLayoutSwitcher={false}
      notificationSlot={null}
      user={session === null ? undefined : { name: session.user.username, email: `Role: ${session.user.role}` }}
      onLogout={() => void logOut()}
      navFooter={
        <>
          {hosts.isError && (
            <p role="status" className="px-3 py-2 text-xs text-destructive-text">
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
      {admin && (
        <>
          <AdminExposureBanner />
          <PanelExposureBanner />
        </>
      )}
      <Outlet />
    </SidebarLayout>
  )
}
