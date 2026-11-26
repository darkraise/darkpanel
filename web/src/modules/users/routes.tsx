import { EmptyState } from "darkraise-ui/components/empty-state"

// The Users and SSH pages under /hosts/:hostId/users/, a placeholder until they are built.
export default function UsersRoutes() {
  return (
    <>
      <h2 className="sr-only">Users and SSH</h2>
      <EmptyState title="Users and SSH" description="This page is not built yet." />
    </>
  )
}
