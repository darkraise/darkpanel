import { useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { formatTime } from "@/lib/format"
import { FirewallChange } from "./firewall-change"
import { FIREWALL_MODULE, snapshotsQueryKey, type SnapshotInfo, type SnapshotList } from "./types"

function Flags({ s }: { s: SnapshotInfo }) {
  return (
    <span className="flex flex-wrap gap-1">
      {s.baseline && <Badge size="sm">baseline</Badge>}
      {s.pinned && <Badge variant="secondary" size="sm">pinned</Badge>}
      {s.system && <Badge variant="outline" size="sm">system</Badge>}
    </span>
  )
}

// RestoreConsent warns before a restore and asks whether it may add or
// remove manual rules: the restore puts UFW's files back as they were, and
// the action refuses to touch a manual rule unless the input says manual.
function RestoreConsent({ snapshot, onCancel, onPlan }: {
  snapshot: SnapshotInfo | null
  onCancel: () => void
  onPlan: (manual: boolean) => void
}) {
  const [manual, setManual] = useState(false)
  return (
    <Dialog
      open={snapshot !== null}
      onOpenChange={(open) => {
        if (open) return
        setManual(false)
        onCancel()
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Restore this snapshot?</DialogTitle>
          <DialogDescription>
            {`UFW's files, the loaded rules and the forwarding settings go back to how they were on ${snapshot === null ? "" : formatTime(snapshot.createdAt)}.`}
          </DialogDescription>
        </DialogHeader>
        <Alert variant="warning" role="status">
          <AlertDescription>
            Rules added by hand since then are removed, and rules deleted by hand are added back. The next step lists every manual rule the restore would add or remove.
          </AlertDescription>
        </Alert>
        <div className="flex items-center gap-2">
          <Checkbox id="restore-manual" checked={manual} onCheckedChange={(checked) => setManual(checked === true)} />
          <Label htmlFor="restore-manual">Allow the restore to add or remove manual rules</Label>
        </div>
        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => {
              setManual(false)
              onCancel()
            }}
          >
            Cancel
          </Button>
          <Button
            onClick={() => {
              onPlan(manual)
              setManual(false)
            }}
          >
            Plan the restore
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// SnapshotsPanel lists the firewall's snapshots, newest first, and
// restores one through the change dialog.
export function SnapshotsPanel({ hostId }: { hostId: string }) {
  const snapshots = useQuery({
    queryKey: snapshotsQueryKey(hostId),
    queryFn: () => readResource<SnapshotList>(hostId, FIREWALL_MODULE, "snapshots"),
  })
  const [asking, setAsking] = useState<SnapshotInfo | null>(null)
  const [restoring, setRestoring] = useState<{ id: string; manual: boolean } | null>(null)

  if (snapshots.isPending) return <Spinner aria-label="Loading the snapshots" />
  if (snapshots.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(snapshots.error)}</AlertDescription>
      </Alert>
    )
  }
  const list = [...snapshots.data.snapshots].sort((a, b) => b.createdAt.localeCompare(a.createdAt))
  if (list.length === 0) return <EmptyState title="No snapshots yet" description="The firewall takes a snapshot before every change." />
  return (
    <>
      <Table aria-label="Firewall snapshots">
        <TableHeader>
          <TableRow>
            <TableHead>Taken</TableHead>
            <TableHead>Before change</TableHead>
            <TableHead>Actor</TableHead>
            <TableHead>Kept</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.map((s) => (
            <TableRow key={s.id}>
              <TableCell>{formatTime(s.createdAt)}</TableCell>
              <TableCell>
                {s.changeId === undefined || s.changeId === "" ? (
                  "—"
                ) : (
                  <Link to={`/changes/${encodeURIComponent(s.changeId)}`} className="font-mono underline-offset-4 hover:underline">
                    {s.changeId}
                  </Link>
                )}
              </TableCell>
              <TableCell className="font-mono">{s.actor}</TableCell>
              <TableCell>
                <Flags s={s} />
              </TableCell>
              <TableCell>
                <Button variant="outline" size="sm" aria-label={`Restore the snapshot taken ${formatTime(s.createdAt)}`} onClick={() => setAsking(s)}>
                  Restore
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <RestoreConsent
        snapshot={asking}
        onCancel={() => setAsking(null)}
        onPlan={(manual) => {
          if (asking !== null) setRestoring({ id: asking.id, manual })
          setAsking(null)
        }}
      />
      <FirewallChange
        hostId={hostId}
        action="firewall.snapshot.restore"
        title="Restore a snapshot"
        input={restoring === null ? null : restoring.manual ? { id: restoring.id, manual: true } : { id: restoring.id }}
        onClose={() => setRestoring(null)}
      />
    </>
  )
}
