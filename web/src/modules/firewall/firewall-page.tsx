import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readResource } from "@/api/client"
import { AddRule } from "./rule-form"
import { RulesTable } from "./rules-table"
import { SnapshotsPanel } from "./snapshots-panel"
import { StateControls } from "./state-controls"
import { StatusBanners, StatusSummary } from "./status-panel"
import { FIREWALL_MODULE, statusQueryKey, type FirewallStatus } from "./types"

// FirewallPage is /hosts/:hostId/firewall: the configured and loaded
// state with its banners, the settings, and the rules.
export function FirewallPage({ hostId }: { hostId: string }) {
  const status = useQuery({
    queryKey: statusQueryKey(hostId),
    queryFn: () => readResource<FirewallStatus>(hostId, FIREWALL_MODULE, "status"),
  })
  if (status.isPending) return <Spinner aria-label="Loading the firewall" />
  if (status.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(status.error)}</AlertDescription>
      </Alert>
    )
  }
  const s = status.data
  return (
    <div className="flex flex-col gap-4">
      <StatusBanners hostId={hostId} status={s} />
      <StatusSummary status={s} />
      <StateControls key={JSON.stringify(s.configured) + String(s.active)} hostId={hostId} status={s} />
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center justify-between gap-4">
            Rules
            <AddRule hostId={hostId} />
          </CardTitle>
        </CardHeader>
        <CardContent>
          <RulesTable hostId={hostId} rules={s.rules} />
        </CardContent>
      </Card>
      <Link to="snapshots" className="text-sm underline-offset-4 hover:underline">
        Snapshots
      </Link>
    </div>
  )
}

// SnapshotsPage is /hosts/:hostId/firewall/snapshots.
export function SnapshotsPage({ hostId }: { hostId: string }) {
  return (
    <div className="flex flex-col gap-4">
      <Link to=".." relative="path" className="text-sm underline-offset-4 hover:underline">
        Back to the firewall
      </Link>
      <Card>
        <CardHeader>
          <CardTitle>Snapshots</CardTitle>
        </CardHeader>
        <CardContent>
          <SnapshotsPanel hostId={hostId} />
        </CardContent>
      </Card>
    </div>
  )
}
