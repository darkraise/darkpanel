import { lazy, Suspense } from "react"
import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { globalPages, moduleRegistry } from "@/modules/registry"
import type { GlobalPage } from "@/modules/types"
import { AlertsPage } from "@/pages/alerts"
import { AuditPage } from "@/pages/audit"
import { ChangePage } from "@/pages/change"
import { EnrollPage } from "@/pages/enroll"
import { GroupsPage } from "@/pages/groups"
import { HostPage } from "@/pages/host"
import { HostsPage } from "@/pages/hosts"
import { LoginPage } from "@/pages/login"
import { NotificationsPage } from "@/pages/notifications"
import { RolloutsPage } from "@/pages/rollouts"
import { SignInSettingsPage } from "@/pages/sign-in-settings"
import { TokensPage } from "@/pages/tokens"
import { UsersPage } from "@/pages/users"
import { RequireAdmin, RequireSession, SessionProvider } from "@/session/session"
import { Shell } from "@/shell"

// The M10 state boards are a development tool: in a production build the
// condition is false, so the route and its chunk are left out.
const M10Boards = import.meta.env.DEV ? lazy(() => import("@/boards/m10-boards").then((m) => ({ default: m.M10Boards }))) : null

function GlobalPageView({ page }: { page: GlobalPage }) {
  const PageRoutes = page.routes
  return (
    <Suspense fallback={<Spinner aria-label={`Loading ${page.label}`} />}>
      <PageRoutes />
    </Suspense>
  )
}

// The routes of spec section 17. Every route but /login, /enroll and the
// development boards sits behind the session guard, the modules' global pages included;
// the admin settings and the Network pages also sit behind the admin guard.
export function App() {
  return (
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/enroll" element={<EnrollPage />} />
        {M10Boards && import.meta.env.DEV && (
          <Route
            path="/__boards/m10"
            element={
              <Suspense fallback={<Spinner aria-label="Loading boards" />}>
                <M10Boards />
              </Suspense>
            }
          />
        )}
        <Route
          element={
            <RequireSession>
              <Shell />
            </RequireSession>
          }
        >
          <Route index element={<HostsPage />} />
          <Route path="hosts/:hostId" element={<HostPage />} />
          <Route path="hosts/:hostId/:moduleId/*" element={<HostPage />} />
          <Route path="alerts" element={<AlertsPage />} />
          <Route path="audit" element={<AuditPage />} />
          <Route path="rollouts" element={<RolloutsPage />} />
          <Route path="changes/:changeId" element={<ChangePage />} />
          <Route path="settings/tokens" element={<TokensPage />} />
          <Route path="settings/users" element={<RequireAdmin><UsersPage /></RequireAdmin>} />
          <Route path="settings/groups" element={<RequireAdmin><GroupsPage /></RequireAdmin>} />
          <Route path="settings/sign-in" element={<RequireAdmin><SignInSettingsPage /></RequireAdmin>} />
          <Route path="settings/notifications" element={<RequireAdmin><NotificationsPage /></RequireAdmin>} />
          {globalPages(moduleRegistry).map((page) => (
            <Route key={page.path} path={`${page.path}/*`} element={<RequireAdmin><GlobalPageView page={page} /></RequireAdmin>} />
          ))}
          <Route path="*" element={<EmptyState title="Page not found" description="Nothing lives at this address." />} />
        </Route>
      </Routes>
    </SessionProvider>
  )
}
