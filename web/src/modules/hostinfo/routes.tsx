import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { SummaryPage } from "./summary-page"

// The host-information module's pages under /hosts/:hostId/hostinfo/.
export default function HostinfoRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<SummaryPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The host-information module has no page at this address." />} />
    </Routes>
  )
}
