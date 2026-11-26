import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { ConsolePage } from "./console-page"

// The console module's pages under /hosts/:hostId/console/.
export default function ConsoleRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<ConsolePage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The console module has no page at this address." />} />
    </Routes>
  )
}
