import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Spinner } from "darkraise-ui/components/spinner"
import { ApiError, errorMessage, readAction } from "@/api/client"
import { DeployDialog } from "./deploy-dialog"
import { DerpChange } from "./derp-change"
import { ProbePanel } from "./probe-panel"
import { RegionDialog } from "./region-dialog"
import { ServersTable, type ServerAction } from "./servers-table"
import { DERP_ACTION_VERSION, derpKey, isDerpServers, isServerProbe, type DerpServerView, type DerpServers, type ServerProbe } from "./types"

type Modal =
  | null
  | { kind: "deploy" }
  | { kind: "region"; server: DerpServerView }
  | { kind: "change"; action: string; title: string; input: Record<string, unknown> }

function readServers(): Promise<DerpServers> {
  return readAction<unknown>("derp.server.list", DERP_ACTION_VERSION, {}).then((body) => {
    if (!isDerpServers(body)) throw new Error("The controller answered with an unexpected server list.")
    return body
  })
}

function readProbe(hostId: string): Promise<ServerProbe> {
  return readAction<unknown>("derp.server.probe", DERP_ACTION_VERSION, { hostId }).then((body) => {
    if (!isServerProbe(body)) throw new Error("The controller answered with an unexpected probe result.")
    return body
  })
}

function messageOf(error: unknown): string {
  if (error instanceof ApiError) return errorMessage(error)
  return error instanceof Error ? error.message : String(error)
}

// RegionStep reads a fresh probe for every opening, so the region dialog never
// shows the warnings of an older one.
function RegionStep({ server, onClose }: { server: DerpServerView; onClose: () => void }) {
  const query = useQuery({
    queryKey: [...derpKey("probe", server.hostId), "region"],
    queryFn: () => readProbe(server.hostId),
    gcTime: 0,
    refetchOnWindowFocus: false,
  })
  if (query.isPending) return <Spinner aria-label="Probing the server" />
  if (query.isError) {
    return (
      <div className="flex flex-col items-start gap-2">
        <Alert variant="destructive" role="alert">
          <AlertDescription>{messageOf(query.error)}</AlertDescription>
        </Alert>
        <Button variant="outline" onClick={onClose}>
          Close
        </Button>
      </div>
    )
  }
  return <RegionDialog server={server} probe={query.data} onClose={onClose} />
}

function PublishedRegions({ servers, onRemove }: { servers: DerpServerView[]; onRemove: (server: DerpServerView) => void }) {
  return (
    <section aria-label="Published regions" className="flex flex-col gap-2">
      <h2 className="text-sm font-medium">Published regions</h2>
      <ul className="flex flex-col gap-2">
        {servers.map((s) => (
          <li key={s.hostId} className="flex items-center gap-3">
            <span>{`${s.regionCode} (${s.regionId}) on ${s.host}`}</span>
            <Button variant="outline" size="sm" aria-label={`Remove region ${s.regionCode} (${s.regionId})`} onClick={() => onRemove(s)}>
              Remove region
            </Button>
          </li>
        ))}
      </ul>
    </section>
  )
}

// DerpPage lists the DERP servers and offers deploying one, probing it,
// publishing or removing its region, upgrading, rotating its certificate and
// removing it.
export function DerpPage() {
  const [probeHost, setProbeHost] = useState<string | null>(null)
  const [modal, setModal] = useState<Modal>(null)
  const query = useQuery({ queryKey: derpKey("servers"), queryFn: readServers })

  function onAction(action: ServerAction, server: DerpServerView) {
    const input = { hostId: server.hostId }
    switch (action) {
      case "probe":
        setProbeHost(server.hostId)
        break
      case "region":
        setModal({ kind: "region", server })
        break
      case "upgrade":
        setModal({ kind: "change", action: "derp.server.upgrade", title: `Upgrade derper on ${server.host}`, input })
        break
      case "rotate":
        setModal({ kind: "change", action: "derp.cert.rotate", title: `Rotate the certificate of ${server.host}`, input })
        break
      case "remove":
        setModal({ kind: "change", action: "derp.server.remove", title: `Remove the DERP server on ${server.host}`, input })
        break
    }
  }

  function removeRegion(server: DerpServerView) {
    setModal({
      kind: "change",
      action: "derp.region.remove",
      title: `Remove DERP region ${server.regionCode} (${server.regionId})`,
      input: { regionId: server.regionId },
    })
  }

  const servers = query.data?.servers ?? []
  const regions = servers.filter((s) => s.regionId !== 0)
  const probed = probeHost === null ? undefined : servers.find((s) => s.hostId === probeHost)
  const close = () => setModal(null)

  return (
    <div className="flex flex-col gap-6">
      <div className="flex justify-end">
        <Button onClick={() => setModal({ kind: "deploy" })}>Deploy a DERP server</Button>
      </div>
      {query.isPending && <Spinner aria-label="Loading the DERP servers" />}
      {query.isError && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{messageOf(query.error)}</AlertDescription>
        </Alert>
      )}
      {query.isSuccess && <ServersTable servers={query.data} onAction={onAction} />}
      {regions.length > 0 && <PublishedRegions servers={regions} onRemove={removeRegion} />}
      {probed !== undefined && (
        <section aria-label={`Probe of ${probed.host}`} className="flex flex-col gap-3">
          <div className="flex items-center justify-between">
            <h2 className="text-sm font-medium">{`Probe of ${probed.host}`}</h2>
            <Button variant="outline" size="sm" onClick={() => setProbeHost(null)}>
              Close probe
            </Button>
          </div>
          <ProbePanel key={probed.hostId} hostId={probed.hostId} />
        </section>
      )}
      {modal?.kind === "deploy" && <DeployDialog onClose={close} />}
      {modal?.kind === "change" && <DerpChange action={modal.action} title={modal.title} input={modal.input} onClose={close} />}
      {modal?.kind === "region" && <RegionStep server={modal.server} onClose={close} />}
    </div>
  )
}
