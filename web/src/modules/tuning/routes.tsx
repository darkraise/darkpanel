import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { TuningPage } from "./tuning-page"

// The tuning module's pages under /hosts/:hostId/tuning/.
export default function TuningRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<TuningPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The tuning module has no page at this address." />} />
    </Routes>
  )
}
