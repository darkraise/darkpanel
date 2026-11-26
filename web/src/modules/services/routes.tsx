import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { ServicesPage } from "./services-page"

// The services module's pages under /hosts/:hostId/services/.
export default function ServicesRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<ServicesPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The services module has no page at this address." />} />
    </Routes>
  )
}
