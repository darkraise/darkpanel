import { EmptyState } from "darkraise-ui/components/empty-state"

// The Tuning pages under /hosts/:hostId/tuning/, a placeholder until they are built.
export default function TuningRoutes() {
  return (
    <>
      <h2 className="sr-only">Tuning</h2>
      <EmptyState title="Tuning" description="This page is not built yet." />
    </>
  )
}
