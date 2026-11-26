import { EmptyState } from "darkraise-ui/components/empty-state"

// The Intrusion prevention pages under /hosts/:hostId/fail2ban/, a placeholder until they are built.
export default function Fail2banRoutes() {
  return (
    <>
      <h2 className="sr-only">Intrusion prevention</h2>
      <EmptyState title="Intrusion prevention" description="This page is not built yet." />
    </>
  )
}
