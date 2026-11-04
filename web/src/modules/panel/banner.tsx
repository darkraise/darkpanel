import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { messageOf, readExposureStatus } from "./status"
import { panelStatusKey } from "./types"

// PanelExposureBanner warns on every page while the panel is exposed beyond
// the tailnet. A failed status read is shown too, because staying silent
// would look the same as "not exposed".
export function PanelExposureBanner() {
  const status = useQuery({ queryKey: panelStatusKey, queryFn: readExposureStatus, staleTime: 60_000 })
  if (status.isError) {
    return (
      <Alert variant="warning" role="status" className="mb-4">
        <AlertTitle>The panel exposure status could not be read</AlertTitle>
        <AlertDescription>{messageOf(status.error)}</AlertDescription>
      </Alert>
    )
  }
  if (!status.isSuccess || !status.data.exposed) return null
  const exposure = status.data.exposure
  if (exposure === null || exposure === undefined) return null
  return (
    <Alert variant="warning" role="status" className="mb-4">
      <AlertTitle>Panel exposed</AlertTitle>
      <AlertDescription>{`Panel exposed via ${exposure.via} at ${exposure.hostname}`}</AlertDescription>
      {exposure.state === "revoked" && <AlertDescription>The listener is revoked, but the removal has not finished.</AlertDescription>}
      {exposure.state === "needs_repair" && <AlertDescription>The exposure needs repair. Remove it again to finish.</AlertDescription>}
      <Link to={`/hosts/${encodeURIComponent(exposure.hostId)}/panel`} className="text-sm underline-offset-4 hover:underline">
        Remove the panel exposure
      </Link>
    </Alert>
  )
}
