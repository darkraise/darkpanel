import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Input } from "darkraise-ui/components/input"
import { Spinner } from "darkraise-ui/components/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { Network } from "lucide-react"
import { ApiError, errorMessage, readResource } from "@/api/client"
import { SectionHeader } from "@/components/visual/section-header"
import { TailnetChange } from "@/modules/tailnet/tailnet-change"
import { LatencyPanel } from "./latency-panel"
import { NodeControls } from "./node-controls"
import { PackagePanel } from "./package-panel"
import { PeersTable } from "./peers-table"
import { PrefsForm } from "./prefs-form"
import { StatusPanel } from "./status-panel"
import { backendPill, TAILSCALE_MODULE, type TailscaleStatus } from "./types"
import { useCanOnHost } from "@/session/can"

const TAG = /^tag:[a-z0-9][a-z0-9-]{0,62}$/

function parseTags(text: string): string[] {
  return text
    .split(",")
    .map((tag) => tag.trim())
    .filter((tag) => tag !== "")
}

function JoinControl({ hostId, install }: { hostId: string; install: boolean }) {
  const client = useQueryClient()
  const [text, setText] = useState("")
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  const tags = parseTags(text)
  const valid = tags.length > 0 && tags.every((tag) => TAG.test(tag))
  return (
    <div className="flex items-center gap-2">
      <Input
        aria-label="Tailscale tags"
        placeholder="tag:server, tag:web"
        value={text}
        onChange={(e) => setText(e.target.value)}
      />
      <Button
        variant="outline"
        disabled={!valid}
        onClick={() => setInput(install ? { hostId, tags, install: true } : { hostId, tags })}
      >
        Join tailnet
      </Button>
      <TailnetChange
        action="tailnet.node.join"
        title="Join the tailnet"
        input={input}
        onClose={() => {
          setInput(null)
          void client.invalidateQueries({ queryKey: ["resource", hostId] })
        }}
      />
    </div>
  )
}

// TailscalePage is /hosts/:hostId/tailscale: the node's status, peers,
// latency, preferences and package, and the join when it is not on a tailnet.
export function TailscalePage({ hostId }: { hostId: string }) {
  const operate = useCanOnHost("operate")
  const admin = useCanOnHost("admin")
  const query = useQuery({
    queryKey: ["resource", hostId, "tailscale.status"],
    queryFn: () => readResource<TailscaleStatus>(hostId, TAILSCALE_MODULE, "status"),
  })

  if (query.isPending) return <Spinner aria-label="Loading Tailscale" />
  if (query.isError) {
    const error = query.error
    const message = error instanceof ApiError ? errorMessage(error) : error.message
    if (error instanceof ApiError && error.code === "tailscale.unavailable") {
      return (
        <div className="flex flex-col gap-4">
          <SectionHeader icon={Network} title="Tailscale" status={{ status: "off", label: "Not available" }} />
          <Alert variant="warning" role="status">
            <AlertDescription>{`Tailscale is not available on this host: ${message}`}</AlertDescription>
          </Alert>
          {admin && <JoinControl hostId={hostId} install />}
          {operate && <PackagePanel hostId={hostId} available={false} />}
        </div>
      )
    }
    return (
      <div className="flex flex-col gap-4">
        <SectionHeader icon={Network} title="Tailscale" />
        <Alert variant="destructive" role="alert">
          <AlertDescription>{message}</AlertDescription>
        </Alert>
      </div>
    )
  }

  const status = query.data
  return (
    <div className="flex flex-col gap-4">
      <SectionHeader icon={Network} title="Tailscale" status={backendPill(status.backendState)} />
      {status.self === null && admin && <JoinControl hostId={hostId} install={false} />}
      <Tabs defaultValue="status">
        <TabsList aria-label="Tailscale">
          <TabsTrigger value="status">Status</TabsTrigger>
          <TabsTrigger value="latency">Latency</TabsTrigger>
          {operate && <TabsTrigger value="preferences">Preferences</TabsTrigger>}
          {operate && <TabsTrigger value="package">Package</TabsTrigger>}
        </TabsList>
        <TabsContent value="status">
          <div className="flex flex-col gap-4">
            <StatusPanel hostId={hostId} />
            <PeersTable peers={status.peers} />
          </div>
        </TabsContent>
        <TabsContent value="latency">
          <LatencyPanel hostId={hostId} />
        </TabsContent>
        {operate && (
          <TabsContent value="preferences">
            <div className="flex flex-col gap-4">
              <PrefsForm hostId={hostId} status={status} prefs={status.prefs ?? {}} />
              <NodeControls hostId={hostId} status={status} />
            </div>
          </TabsContent>
        )}
        {operate && (
          <TabsContent value="package">
            <PackagePanel hostId={hostId} available />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}
