import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { JobsPage } from "./jobs-page"

// The jobs module's pages under /hosts/:hostId/jobs/.
export default function JobsRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<JobsPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The jobs module has no page at this address." />} />
    </Routes>
  )
}
