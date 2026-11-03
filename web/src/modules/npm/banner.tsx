import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { readAction } from "@/api/client"
import { isInstancesOutput, NPM_ACTION_VERSION, npmFleetKey, type InstancesOutput } from "./types"

function readInstances(): Promise<InstancesOutput> {
  return readAction<unknown>("npm.instance.list", NPM_ACTION_VERSION, {}).then((body) => {
    if (!isInstancesOutput(body)) throw new Error("The controller answered with an unexpected NPM instance list.")
    return body
  })
}

// AdminExposureBanner warns on every page about each host whose NPM admin
// interface is exposed. It stays silent while the fleet read is pending or
// has failed: the banner is a reminder, and the NPM page reports the errors.
export function AdminExposureBanner() {
  const fleet = useQuery({ queryKey: npmFleetKey, queryFn: readInstances, staleTime: 60_000 })
  if (!fleet.isSuccess) return null
  const exposed = fleet.data.instances.flatMap((i) => {
    const exposure = i.status.adminExposure
    return exposure === null || exposure === undefined ? [] : [{ instance: i, hostname: exposure.hostname }]
  })
  if (exposed.length === 0) return null
  return (
    <>
      {exposed.map(({ instance, hostname }) => (
        <Alert key={instance.hostId} variant="warning" role="status" className="mb-4">
          <AlertTitle>Admin interface exposed</AlertTitle>
          <AlertDescription>{`The NPM admin interface of ${instance.hostName} is exposed at https://${hostname}.`}</AlertDescription>
          <Link to={`/hosts/${encodeURIComponent(instance.hostId)}/npm`} className="text-sm underline-offset-4 hover:underline">
            {`Open Nginx Proxy Manager on ${instance.hostName}`}
          </Link>
        </Alert>
      ))}
    </>
  )
}
