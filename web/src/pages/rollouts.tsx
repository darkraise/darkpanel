import { useId, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { HOSTS_QUERY_KEY, ROLLOUTS_QUERY_KEY, changeError, changeInProgress, errorMessage, listHosts, listRollouts } from "@/api/client"
import type { Rollout } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { stateVariant } from "@/lib/badges"
import { formatTime } from "@/lib/format"
import { usePageTitle } from "@/lib/page-title"
import { StartRolloutButton } from "./start-rollout-dialog"

const ACTIVE = new Set(["running", "draining"])

type Phase = "closed" | "form" | "plan"

function CancelRollout({ rollout }: { rollout: Rollout }) {
  const client = useQueryClient()
  const [phase, setPhase] = useState<Phase>("closed")
  const [force, setForce] = useState(false)

  function reset() {
    setPhase("closed")
    setForce(false)
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPhase("plan")
  }

  return (
    <>
      <Button variant="outline" onClick={() => setPhase("form")}>
        Cancel rollout
      </Button>
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? undefined : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{`Cancel the rollout of ${rollout.version}`}</DialogTitle>
            <DialogDescription>Hosts not yet updated are skipped. A host that is updating finishes first unless you cancel without waiting.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <div className="flex items-center gap-2">
              <Checkbox id={`cancel-force-${rollout.id}`} checked={force} onCheckedChange={(c) => setForce(c === true)} />
              <Label htmlFor={`cancel-force-${rollout.id}`}>Cancel without waiting for the host that is updating</Label>
            </div>
            <DialogFooter>
              <Button type="submit">Continue</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ChangeDialog
        open={phase === "plan"}
        onOpenChange={(next) => (next ? undefined : reset())}
        title={`Cancel the rollout of ${rollout.version}`}
        target={{ name: "core.update.rollout-cancel", version: 1 }}
        input={{ rolloutId: rollout.id, force }}
        onApplied={(change) => {
          if (changeError(change) === null && !changeInProgress(change)) void client.invalidateQueries({ queryKey: ROLLOUTS_QUERY_KEY })
        }}
      />
    </>
  )
}

function RolloutCard({ rollout, names }: { rollout: Rollout; names: Map<string, string> }) {
  const id = useId()
  return (
    <section aria-labelledby={id}>
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle>
              <h2 id={id} className="break-all">{`${rollout.version} (${rollout.channel})`}</h2>
            </CardTitle>
            <Badge variant={stateVariant(rollout.state)} size="sm">
              {rollout.state}
            </Badge>
          </div>
          {ACTIVE.has(rollout.state) && <CancelRollout rollout={rollout} />}
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <p className="text-xs text-muted-foreground">
            {`Created ${formatTime(rollout.createdAt)} · updated ${formatTime(rollout.updatedAt)}`}
          </p>
          {rollout.error !== null && (
            <Alert variant="destructive">
              <AlertDescription>{rollout.error.message}</AlertDescription>
            </Alert>
          )}
          <div className="overflow-x-auto">
            {/* darkraise-ui lets table cells break anywhere, so without a minimum width
                a narrow table squeezes its words apart instead of scrolling. */}
            <Table className="min-w-[32rem]">
              <TableHeader>
                <TableRow>
                  <TableHead>#</TableHead>
                  <TableHead>Host</TableHead>
                  <TableHead>State</TableHead>
                  <TableHead>Error</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rollout.hosts.map((h) => (
                  <TableRow key={h.hostId}>
                    <TableCell className="whitespace-nowrap">{h.position + 1}</TableCell>
                    <TableCell className="break-all">{names.get(h.hostId) ?? h.hostId}</TableCell>
                    <TableCell>
                      <Badge variant={stateVariant(h.state)} size="sm">
                        {h.state}
                      </Badge>
                    </TableCell>
                    <TableCell>{h.error?.message ?? "—"}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
    </section>
  )
}

function RolloutsBody() {
  const rollouts = useQuery({
    queryKey: ROLLOUTS_QUERY_KEY,
    queryFn: listRollouts,
    refetchInterval: (query) => (query.state.data?.rollouts.some((r) => ACTIVE.has(r.state)) ? 5000 : false),
  })
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  if (rollouts.isPending) return <Spinner aria-label="Loading the rollouts" />
  if (rollouts.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(rollouts.error)}</AlertDescription>
      </Alert>
    )
  }
  if (rollouts.data.rollouts.length === 0) {
    // darkraise-ui's EmptyState title is an h3, so a hidden h2 keeps the
    // heading levels under the page's h1 from skipping one.
    return (
      <>
        <h2 className="sr-only">Rollout list</h2>
        <EmptyState title="No rollouts yet" action={<StartRolloutButton />} />
      </>
    )
  }
  const names = new Map((hosts.data?.hosts ?? []).map((h) => [h.id, h.name]))
  return (
    <div className="flex flex-col gap-4">
      {rollouts.data.rollouts.map((r) => (
        <RolloutCard key={r.id} rollout={r} names={names} />
      ))}
    </div>
  )
}

export function RolloutsPage() {
  usePageTitle("Rollouts")
  return (
    <>
      <PageHeader
        title="Rollouts"
        actions={
          <div className="flex flex-wrap gap-2">
            <StartRolloutButton />
          </div>
        }
      />
      <RolloutsBody />
    </>
  )
}
