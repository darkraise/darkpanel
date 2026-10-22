import { useEffect, useState } from "react"
import { Outlet } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { History, KeyRound, Server } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { SidebarLayout, type NavGroup } from "darkraise-ui/layout"
import { errorMessage, getListenerState } from "@/api/client"
import { connectEvents } from "@/api/events"
import { useSession } from "@/session/use-session"

const NAV: NavGroup[] = [
  {
    items: [
      { label: "Hosts", href: "/", icon: Server },
      { label: "Audit log", href: "/audit", icon: History },
    ],
  },
  { label: "Settings", items: [{ label: "API tokens", href: "/settings/tokens", icon: KeyRound }] },
]

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
      nav={NAV}
      showThemeSwitcher
      showLayoutSwitcher={false}
      notificationSlot={null}
      user={session === null ? undefined : { name: session.user.username, email: `Role: ${session.user.role}` }}
      onLogout={() => void logOut()}
      navFooter={<ListenerNotice />}
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
