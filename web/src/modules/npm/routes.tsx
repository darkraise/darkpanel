import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { NpmPage } from "./npm-page"

// The Nginx Proxy Manager module's pages under /hosts/:hostId/npm/.
export default function NpmRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      {/* The host page keeps this element mounted when the host changes; the key drops the previous host's dialogs,
          pending changes and any admin password still being revealed for it. */}
      <Route index element={<NpmPage key={hostId} hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The Nginx Proxy Manager module has no page at this address." />} />
    </Routes>
  )
}
