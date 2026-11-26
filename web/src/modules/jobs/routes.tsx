import { EmptyState } from "darkraise-ui/components/empty-state"

// The Jobs pages under /hosts/:hostId/jobs/, a placeholder until they are built.
export default function JobsRoutes() {
  return (
    <>
      <h2 className="sr-only">Jobs</h2>
      <EmptyState title="Jobs" description="This page is not built yet." />
    </>
  )
}
