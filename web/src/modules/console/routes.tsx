import { EmptyState } from "darkraise-ui/components/empty-state"

// The Console pages under /hosts/:hostId/console/, a placeholder until they are built.
export default function ConsoleRoutes() {
  return (
    <>
      <h2 className="sr-only">Console</h2>
      <EmptyState title="Console" description="This page is not built yet." />
    </>
  )
}
