import { Badge } from "darkraise-ui/components/badge"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { formatTime } from "@/lib/format"
import type { TailscalePeer } from "./types"

function connectionText(p: TailscalePeer): string {
  if (p.connection === "direct") return `direct ${p.endpoint}`
  if (p.connection === "derp") return `derp ${p.region}`
  return p.connection
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
              <Badge variant={p.online ? "default" : "secondary"} size="sm">
                {p.online ? "Online" : "Offline"}
              </Badge>
            </TableCell>
            <TableCell>{connectionText(p)}</TableCell>
            <TableCell>{exitNodeText(p)}</TableCell>
            <TableCell>{formatTime(p.lastHandshake)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}
