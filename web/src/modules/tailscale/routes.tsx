import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { TailscalePage } from "./tailscale-page"

// The Tailscale module's pages under /hosts/:hostId/tailscale/.
export default function TailscaleRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<TailscalePage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The Tailscale module has no page at this address." />} />
    </Routes>
  )
}
