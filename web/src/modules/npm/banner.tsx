import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { HOSTS_QUERY_KEY, listHosts, readAction } from "@/api/client"
import { isInstancesOutput, NPM_ACTION_VERSION, npmFleetKey, type InstancesOutput } from "./types"

function readInstances(): Promise<InstancesOutput> {
  return readAction<unknown>("npm.instance.list", NPM_ACTION_VERSION, {}).then((body) => {
    if (!isInstancesOutput(body)) throw new Error("The controller answered with an unexpected NPM instance list.")
    return body
  })
}

function NotCheckedAlert({ text }: { text: string }) {
  return (
    <Alert variant="warning" role="status" className="mb-4">
      <AlertTitle>Admin exposure not checked</AlertTitle>
      <AlertDescription>{text}</AlertDescription>
    </Alert>
  )
}

// AdminExposureBanner warns on every page about each host whose NPM admin
// interface is exposed. It also warns when the exposure of a host could not
// be checked, or when the whole fleet read failed, so that a missing warning
// never hides an unchecked host. It shows nothing while the first read is
// pending. When a later read fails, it keeps the warnings of the last
// successful read and adds the could-not-check warning beside them.
export function AdminExposureBanner() {
  const fleet = useQuery({ queryKey: npmFleetKey, queryFn: readInstances, staleTime: 60_000 })
  const unreadable = fleet.data?.unreadable ?? []
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts, enabled: unreadable.length > 0 })
  if (fleet.data === undefined) {
    return fleet.isError ? <NotCheckedAlert text="Could not check whether the NPM admin UI is exposed." /> : null
  }
  const exposed = fleet.data.instances.flatMap((i) => {
    const exposure = i.status.adminExposure
    return exposure === null || exposure === undefined ? [] : [{ instance: i, hostname: exposure.hostname }]
  })
  const names = unreadable.map((id) => hosts.data?.hosts.find((h) => h.id === id)?.name ?? id)
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
      {names.length > 0 && <NotCheckedAlert text={`Could not check whether the NPM admin UI is exposed on: ${names.join(", ")}.`} />}
      {fleet.isError && <NotCheckedAlert text="Could not check whether the NPM admin UI is exposed." />}
    </>
  )
}
