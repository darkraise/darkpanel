import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { FirewallPage, SnapshotsPage } from "./firewall-page"

// The firewall module's pages under /hosts/:hostId/firewall/.
export default function FirewallRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<FirewallPage hostId={hostId} />} />
      <Route path="snapshots" element={<SnapshotsPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The firewall module has no page at this address." />} />
    </Routes>
  )
}
