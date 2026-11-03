import { useQuery } from "@tanstack/react-query"
import { Link } from "react-router"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readAction, readResource } from "@/api/client"
import { DOCKER_ACTION_VERSION, DOCKER_MODULE, dockerKey, type ContainerDetail, type DockerNetwork } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { connectorListKey, type ConnectorList } from "./connector-types"
import { ExposeTunnelForm } from "./expose-dialog"
import { CLOUDFLARE_ACTION_VERSION, cloudflareKey, type TunnelView } from "./types"

// ExposeAction publishes a container through a Cloudflare tunnel whose
// connector runs on the container's host, with cloudflare.expose.container.
export function ExposeAction({ container, onDone }: { container: ContainerRef; onDone: () => void }) {
  const { hostId, containerId } = container
  const tunnels = useQuery({
    queryKey: cloudflareKey("tunnels"),
    queryFn: () => readAction<{ tunnels: TunnelView[] }>("cloudflare.tunnel.list", CLOUDFLARE_ACTION_VERSION, {}),
  })
  const connectors = useQuery({
    queryKey: connectorListKey(hostId),
    queryFn: () => readAction<ConnectorList>("cloudflare.connector.list", CLOUDFLARE_ACTION_VERSION, {}, hostId),
  })
  const detail = useQuery({
    queryKey: ["resource", hostId, "docker.container", containerId],
    queryFn: () => readAction<ContainerDetail>("docker.container.inspect", DOCKER_ACTION_VERSION, { container: containerId }, hostId),
  })
  const networks = useQuery({
    queryKey: dockerKey(hostId, "networks"),
    queryFn: () => readResource<{ networks: DockerNetwork[] }>(hostId, DOCKER_MODULE, "networks"),
  })
  if (tunnels.isPending || connectors.isPending || detail.isPending || networks.isPending) {
    return <Spinner aria-label="Loading the tunnels and the container's networks" />
  }
  if (tunnels.isError || connectors.isError || detail.isError || networks.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(tunnels.error ?? connectors.error ?? detail.error ?? networks.error)}</AlertDescription>
      </Alert>
    )
  }
  if (tunnels.data.tunnels.length === 0) {
    return (
      <>
        <p>No Cloudflare tunnel exists yet. Create a tunnel first.</p>
        <Link to="/cloudflare/tunnels">Open the tunnels page</Link>
      </>
    )
  }
  // A connector whose container is gone cannot take the attach: the controller refuses it with cloudflare.connector_missing.
  const running = new Set(connectors.data.connectors.filter((c) => c.state !== "missing").map((c) => c.tunnel))
  const offered = tunnels.data.tunnels.filter((t) => running.has(t.id)).map((t) => ({ id: t.id, name: t.name }))
  if (offered.length === 0) {
    return (
      <>
        <p>No Cloudflare connector runs on this host. Deploy one first.</p>
        <Link to="/cloudflare/connectors">Open the connectors page</Link>
      </>
    )
  }
  return (
    <ExposeTunnelForm
      container={container}
      tunnels={offered}
      endpoints={detail.data.networks.map((e) => e.network)}
      networks={networks.data.networks}
      ports={detail.data.ports.map((p) => p.privatePort)}
      onDone={onDone}
    />
  )
}
