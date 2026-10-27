import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readAction } from "@/api/client"
import { formatTime } from "@/lib/format"
import { FIREWALL_ACTION_VERSION, type SnapshotComparison, type SnapshotInfo } from "./types"

// ComparePair is what to compare: snapshot a against snapshot b, or against
// the host's current state when b is null.
export interface ComparePair {
  a: SnapshotInfo
  b: SnapshotInfo | null
}

function Comparison({ hostId, pair }: { hostId: string; pair: ComparePair }) {
  const input = pair.b === null ? { from: pair.a.id } : { from: pair.a.id, to: pair.b.id }
  const compared = useQuery({
    queryKey: ["firewall.snapshot.compare", hostId, pair.a.id, pair.b?.id ?? null],
    queryFn: () => readAction<SnapshotComparison>("firewall.snapshot.compare", FIREWALL_ACTION_VERSION, input, hostId),
    gcTime: 0,
  })
  if (compared.isPending) return <Spinner aria-label="Comparing" />
  if (compared.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(compared.error)}</AlertDescription>
      </Alert>
    )
  }
  const { diff, files } = compared.data
  if (diff.length === 0 && files.length === 0) return <p>No differences.</p>
  return (
    <div className="flex flex-col gap-3">
      {diff.length > 0 && (
        <pre aria-label="Differences" className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">
          {diff.map((d, i) => (
            <div key={i} className={d.op === "+" ? "text-success" : d.op === "-" ? "text-destructive" : undefined}>
              {`${d.op} ${d.text}`}
            </div>
          ))}
        </pre>
      )}
      {files.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="text-sm font-medium">Files that differ</span>
          <ul aria-label="Files that differ" className="list-disc pl-5 font-mono text-xs">
            {files.map((f) => (
              <li key={f.path}>{f.path}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}

// CompareDialog shows firewall.snapshot.compare's diff of two snapshots,
// or of one snapshot against now.
export function CompareDialog({ hostId, pair, onClose }: { hostId: string; pair: ComparePair | null; onClose: () => void }) {
  return (
    <Dialog open={pair !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{pair?.b === null ? "Compare with now" : "Compare two snapshots"}</DialogTitle>
          <DialogDescription>
            {pair === null
              ? ""
              : pair.b === null
                ? `From the snapshot taken ${formatTime(pair.a.createdAt)} to the firewall as it is now.`
                : `From the snapshot taken ${formatTime(pair.a.createdAt)} to the one taken ${formatTime(pair.b.createdAt)}.`}
          </DialogDescription>
        </DialogHeader>
        {pair !== null && <Comparison hostId={hostId} pair={pair} />}
      </DialogContent>
    </Dialog>
  )
}
