import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { AuditPage } from "@/pages/audit"
import { HostPage } from "@/pages/host"
import { HostsPage } from "@/pages/hosts"
import { LoginPage } from "@/pages/login"
import { TokensPage } from "@/pages/tokens"
import { RequireSession, SessionProvider } from "@/session/session"
import { Shell } from "@/shell"

// The routes of spec section 17. Every route but /login sits behind the
// session guard.
export function App() {
  return (
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
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
          <Route path="audit" element={<AuditPage />} />
          <Route path="settings/tokens" element={<TokensPage />} />
          <Route path="*" element={<EmptyState title="Page not found" description="Nothing lives at this address." />} />
        </Route>
      </Routes>
    </SessionProvider>
  )
}
