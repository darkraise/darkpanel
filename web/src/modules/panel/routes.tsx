import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { PanelPage } from "./panel-page"

// The panel module's pages under /hosts/:hostId/panel/.
export default function PanelRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      {/* The host page keeps this element mounted when the host changes; the key drops the previous host's form and
          pending change. */}
      <Route index element={<PanelPage key={hostId} hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The panel module has no page at this address." />} />
    </Routes>
  )
}
