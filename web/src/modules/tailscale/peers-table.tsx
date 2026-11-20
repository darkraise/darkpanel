import { EmptyState } from "darkraise-ui/components/empty-state"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { ArrowLeftRight, LogOut, RadioTower, Unplug, Waypoints, type LucideIcon } from "lucide-react"
import { StatusPill } from "@/components/visual/status-pill"
import { formatTime } from "@/lib/format"
import type { TailscalePeer } from "./types"

function connectionText(p: TailscalePeer): string {
  if (p.connection === "direct") return `direct ${p.endpoint}`
  if (p.connection === "derp") return `derp ${p.region}`
  return p.connection
}

const connectionIcon: Record<string, LucideIcon> = {
  direct: ArrowLeftRight,
  derp: RadioTower,
  "peer-relay": Waypoints,
}

function Connection({ peer }: { peer: TailscalePeer }) {
  const Icon = connectionIcon[peer.connection] ?? Unplug
  return (
    <span className="inline-flex items-center gap-1.5">
      <Icon aria-hidden className="text-muted-foreground size-4 shrink-0" />
      {connectionText(peer)}
    </span>
  )
}

function exitNodeText(p: TailscalePeer): string {
  if (p.exitNode) return "In use"
  return p.exitNodeOption ? "Available" : "—"
}

// PeersTable lists the tailnet peers in the order given.
export function PeersTable({ peers }: { peers: TailscalePeer[] }) {
  if (peers.length === 0) return <EmptyState title="No peers" />
  return (
    <Table aria-label="Tailscale peers">
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Addresses</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>Connection</TableHead>
          <TableHead>Exit node</TableHead>
          <TableHead>Last handshake</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {peers.map((p) => (
          <TableRow key={p.id}>
            <TableCell>{p.dnsName === "" ? p.hostName : p.dnsName}</TableCell>
            <TableCell>{p.addresses.join(", ")}</TableCell>
            <TableCell>
              <StatusPill status={p.online ? "ok" : "off"} label={p.online ? "Online" : "Offline"} />
            </TableCell>
            <TableCell>
              <Connection peer={p} />
            </TableCell>
            <TableCell>
              {p.exitNode ? (
                <span className="inline-flex items-center gap-1.5">
                  <LogOut aria-hidden className="text-muted-foreground size-4 shrink-0" />
                  {exitNodeText(p)}
                </span>
              ) : (
                exitNodeText(p)
              )}
            </TableCell>
            <TableCell>{formatTime(p.lastHandshake)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
