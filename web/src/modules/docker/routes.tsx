import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { DockerPage } from "./docker-page"

// The Docker module's pages under /hosts/:hostId/docker/.
export default function DockerRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<DockerPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The Docker module has no page at this address." />} />
    </Routes>
  )
}
