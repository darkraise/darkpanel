import { Route, Routes } from "react-router"
import { EmptyState } from "darkraise-ui/components/empty-state"
import type { ModuleRouteProps } from "@/modules/types"
import { UsersPage } from "./users-page"

// The users module's pages under /hosts/:hostId/users/.
export default function UsersRoutes({ hostId }: ModuleRouteProps) {
  return (
    <Routes>
      <Route index element={<UsersPage hostId={hostId} />} />
      <Route path="*" element={<EmptyState title="Page not found" description="The users module has no page at this address." />} />
    </Routes>
  )
}
