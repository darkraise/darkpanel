import { EmptyState } from "darkraise-ui/components/empty-state"

// The Services pages under /hosts/:hostId/services/, a placeholder until they are built.
export default function ServicesRoutes() {
  return (
    <>
      <h2 className="sr-only">Services</h2>
      <EmptyState title="Services" description="This page is not built yet." />
    </>
  )
}
