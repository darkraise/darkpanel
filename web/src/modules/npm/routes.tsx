import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { NpmPage } from "./npm-page"

// The Nginx Proxy Manager module's pages under /hosts/:hostId/npm/.
export default function NpmRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<NpmPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The Nginx Proxy Manager module has no page at this address." />} />
    </Routes>
  )
}
