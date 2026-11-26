import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { Fail2banPage } from "./fail2ban-page"

// The fail2ban module's pages under /hosts/:hostId/fail2ban/.
export default function Fail2banRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<Fail2banPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The fail2ban module has no page at this address." />} />
    </Routes>
  )
}
