import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { SystemPage } from "./system-page"

// The System module's pages under /hosts/:hostId/system/.
export default function SystemRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<SystemPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The System module has no page at this address." />} />
    </Routes>
  )
}
