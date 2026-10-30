import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readAction } from "@/api/client"
import { formatTime } from "@/lib/format"
import { CloudflareChange } from "./cloudflare-change"
import { CLOUDFLARE_ACTION_VERSION, cloudflareKey, type TunnelView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

function connections(t: TunnelView): string {
  if (t.connections.length === 0) return "0"
  return `${t.connections.length} (${t.connections.map((c) => c.colo).join(", ")})`
}

function TunnelsTable({ tunnels, onPlan }: { tunnels: TunnelView[]; onPlan: (pending: Pending) => void }) {
  if (tunnels.length === 0) return <EmptyState title="No tunnels yet" description="Create a tunnel to publish a service." />
  return (
    <Table aria-label="Tunnels">
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Connections</TableHead>
          <TableHead>Hostnames</TableHead>
          <TableHead>Configuration</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {tunnels.map((t) => (
          <TableRow key={t.id}>
            <TableCell className="font-mono">{t.name}</TableCell>
            <TableCell>
              <Badge variant={t.status === "healthy" ? "default" : "secondary"}>{t.status}</Badge>
            </TableCell>
            <TableCell>{connections(t)}</TableCell>
            <TableCell>{t.hostnames}</TableCell>
            <TableCell>{t.drift ? "Changed outside Darkpanel" : "In sync"}</TableCell>
            <TableCell>{formatTime(t.createdAt)}</TableCell>
            <TableCell>
              <div className="flex justify-end gap-2">
                {t.drift && (
                  <Button
                    variant="outline"
                    size="sm"
                    aria-label={`Accept the current configuration of ${t.name}`}
                    onClick={() => onPlan({ action: "cloudflare.tunnel.adopt", title: `Accept the configuration of ${t.name}`, input: { tunnel: t.id } })}
                  >
                    Accept configuration
                  </Button>
                )}
                <Button
                  variant="outline"
                  size="sm"
                  aria-label={`Delete tunnel ${t.name}`}
                  onClick={() => onPlan({ action: "cloudflare.tunnel.delete", title: `Delete tunnel ${t.name}`, input: { tunnel: t.name } })}
                >
                  Delete
                </Button>
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function TunnelsPage() {
  const tunnels = useQuery({
    queryKey: cloudflareKey("tunnels"),
    queryFn: () => readAction<{ tunnels: TunnelView[] }>("cloudflare.tunnel.list", CLOUDFLARE_ACTION_VERSION, {}),
  })
  const [pending, setPending] = useState<Pending | null>(null)

  if (tunnels.isPending) return <Spinner aria-label="Loading the tunnels" />
  if (tunnels.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(tunnels.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <>
      <Card>
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle>Tunnels</CardTitle>
          <Button onClick={() => setPending({ action: "cloudflare.tunnel.create", title: "Create a tunnel", input: {} })}>Create tunnel</Button>
        </CardHeader>
        <CardContent>
          <TunnelsTable tunnels={tunnels.data.tunnels} onPlan={setPending} />
        </CardContent>
      </Card>
      {pending !== null && <CloudflareChange action={pending.action} title={pending.title} input={pending.input} onClose={() => setPending(null)} />}
    </>
  )
}
