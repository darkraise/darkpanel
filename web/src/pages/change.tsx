import { useState, type ReactNode } from "react"
import { Link, useParams } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import {
  changeQueryKey,
  effectsQueryKey,
  errorMessage,
  getChange,
  listEffects,
  listSteps,
  observeEffect,
  resolveEffect,
  stepsQueryKey,
} from "@/api/client"
import type { Change, Effect, StateDoc } from "@/api/types"
import { formatTime } from "@/lib/format"
import { ChangeActions } from "./change-actions"

function stateVariant(state: string): BadgeVariant {
  switch (state) {
    case "completed":
    case "confirmed":
    case "made":
    case "undone":
    case "accepted":
      return "default"
    case "failed":
    case "needs_repair":
    case "interrupted":
    case "unresolved":
      return "destructive"
    default:
      return "secondary"
  }
}

function ChangeLink({ id }: { id: string }) {
  return (
    <Link to={`/changes/${encodeURIComponent(id)}`} className="font-mono underline-offset-4 hover:underline">
      {id}
    </Link>
  )
}

function Doc({ label, doc }: { label: string; doc: StateDoc | null }) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-sm font-medium">{label}</span>
      <pre aria-label={label} className="overflow-x-auto rounded-md bg-muted p-2 font-mono text-xs">
        {doc === null ? "—" : JSON.stringify(doc.state, null, 2)}
      </pre>
    </div>
  )
}

function Summary({ change }: { change: Change }) {
  const rows: Array<[string, ReactNode]> = [
    ["State", <Badge variant={stateVariant(change.state)}>{change.state}</Badge>],
    ["Class", change.class],
    ["Host", change.hostId ?? "The controller"],
    ["Actor", <span className="font-mono">{change.actor}</span>],
    ["Created", formatTime(change.createdAt)],
    ["Updated", formatTime(change.updatedAt)],
  ]
  if (change.revertDeadline !== null) rows.push(["Revert deadline", formatTime(change.revertDeadline)])
  if (change.owner !== null) rows.push(["Being worked on by", change.owner])
  if (change.parentChangeId !== null) rows.push(["Step of", <ChangeLink id={change.parentChangeId} />])
  if (change.repairedBy !== null) rows.push(["Repaired by", <ChangeLink id={change.repairedBy} />])
  if (change.repairOutcome !== null) rows.push(["Repair outcome", change.repairOutcome])
  if (change.compensatedBy !== null) rows.push(["Compensated by", <span className="font-mono">{change.compensatedBy}</span>])
  return (
    <div className="flex flex-col gap-4">
      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 text-sm">
        {rows.map(([term, value]) => (
          <div key={term} className="contents">
            <dt className="text-muted-foreground">{term}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
      {change.error !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>
            <p>
              <span className="font-mono">{change.error.code}</span>
              {`: ${change.error.message}`}
            </p>
            {change.error.cause !== undefined && change.error.cause !== "" && (
              <p>
                {"Cause: "}
                <span className="font-mono">{change.error.cause}</span>
              </p>
            )}
          </AlertDescription>
        </Alert>
      )}
    </div>
  )
}

function Steps({ changeId }: { changeId: string }) {
  const steps = useQuery({ queryKey: stepsQueryKey(changeId), queryFn: () => listSteps(changeId) })
  if (steps.isPending) return <Spinner aria-label="Loading the steps" />
  if (steps.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(steps.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <section aria-label="Steps" className="flex flex-col gap-2">
      <h2 className="text-lg font-semibold">Steps</h2>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>#</TableHead>
            <TableHead>Step</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Change</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {[...steps.data].sort((a, b) => a.ordinal - b.ordinal).map((s) => (
            <TableRow key={s.id}>
              <TableCell>{s.ordinal}</TableCell>
              <TableCell className="font-mono">{s.id}</TableCell>
              <TableCell className="font-mono">{s.action}</TableCell>
              <TableCell>
                <Badge variant={stateVariant(s.state)} size="sm">
                  {s.state}
                </Badge>
                {s.error !== null && <p className="text-xs text-destructive">{s.error.message}</p>}
              </TableCell>
              <TableCell>{s.childChangeId === "" ? "—" : <ChangeLink id={s.childChangeId} />}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  )
}

// ResolveDialog shows what an unresolved effect's resource looks like now,
// read without changing anything, and only then offers the resolutions the
// module allows.
function ResolveDialog({ effect, onClose }: { effect: Effect; onClose: () => void }) {
  const client = useQueryClient()
  const observed = useQuery({
    queryKey: ["observe", effect.changeId, effect.seq],
    queryFn: () => observeEffect(effect.changeId, effect.seq),
    staleTime: 0,
    gcTime: 0,
  })
  const [resource, setResource] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function resolve(resolution: "accept" | "reconcile") {
    setBusy(true)
    setError(null)
    try {
      await resolveEffect(effect.changeId, effect.seq, resource === "" ? { resolution } : { resolution, resource })
      void client.invalidateQueries({ queryKey: effectsQueryKey(effect.changeId) })
      void client.invalidateQueries({ queryKey: changeQueryKey(effect.changeId) })
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const obs = observed.data?.observation
  const allowed = observed.data?.allowed ?? ["accept", "reconcile"]
  const candidates = obs?.candidates ?? []
  const needsChoice = candidates.length > 1 && resource === ""
  return (
    <Dialog open onOpenChange={(next) => (next || busy ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Resolve effect ${effect.seq}`}</DialogTitle>
          <DialogDescription>{`${effect.kind} ${effect.resource}`}</DialogDescription>
        </DialogHeader>
        {observed.isPending ? (
          <Spinner aria-label="Observing the resource" />
        ) : observed.isError ? (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{errorMessage(observed.error)}</AlertDescription>
          </Alert>
        ) : (
          <div className="flex flex-col gap-3">
            <Doc label="Before" doc={effect.before} />
            <Doc label="Intended" doc={effect.after} />
            <Doc label="Now" doc={obs?.current ?? null} />
            {candidates.length > 1 && (
              <fieldset className="flex flex-col gap-2">
                <legend className="text-sm font-medium">{`${obs?.matches ?? candidates.length} objects match; pick the one this change made`}</legend>
                <RadioGroup value={resource} onValueChange={setResource} aria-label="Matching objects">
                  {candidates.map((c) => (
                    <div key={c.resource} className="flex items-center gap-2">
                      <RadioGroupItem id={`candidate-${c.resource}`} value={c.resource} />
                      <Label htmlFor={`candidate-${c.resource}`} className="font-mono">
                        {c.resource}
                      </Label>
                    </div>
                  ))}
                </RadioGroup>
              </fieldset>
            )}
          </div>
        )}
        {error !== null && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          {allowed.includes("accept") && (
            <Button variant="outline" disabled={observed.data === undefined || busy} onClick={() => void resolve("accept")}>
              Accept as it is
            </Button>
          )}
          {allowed.includes("reconcile") && (
            <Button disabled={observed.data === undefined || busy || needsChoice} onClick={() => void resolve("reconcile")}>
              Reconcile
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function Effects({ change }: { change: Change }) {
  // Change events refetch the change, not its effects; keying the effects
  // on the change's last update refetches them whenever the change moved on.
  const effects = useQuery({
    queryKey: [...effectsQueryKey(change.id), change.updatedAt, change.state],
    queryFn: () => listEffects(change.id),
  })
  const [resolving, setResolving] = useState<Effect | null>(null)

  if (effects.isPending) return <Spinner aria-label="Loading the effects" />
  if (effects.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(effects.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <section aria-label="Effects" className="flex flex-col gap-2">
      <h2 className="text-lg font-semibold">Effects</h2>
      {effects.data.length === 0 ? (
        <p className="text-sm text-muted-foreground">This change recorded no effects.</p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>#</TableHead>
              <TableHead>Kind</TableHead>
              <TableHead>Resource</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Resolution</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {effects.data.map((e) => (
              <TableRow key={`${e.changeId}-${e.seq}`}>
                <TableCell>{e.seq}</TableCell>
                <TableCell>{e.kind}</TableCell>
                <TableCell className="font-mono">{e.resource}</TableCell>
                <TableCell>
                  <Badge variant={stateVariant(e.state)} size="sm">
                    {e.reason === "" ? e.state : `${e.state}: ${e.reason}`}
                  </Badge>
                </TableCell>
                <TableCell>
                  {e.state === "unresolved" ? (
                    <Button size="sm" variant="outline" onClick={() => setResolving(e)}>
                      {`Preview effect ${e.seq}`}
                    </Button>
                  ) : e.resolution === "" ? (
                    "—"
                  ) : (
                    `${e.resolution} by ${e.resolvedBy}`
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {resolving !== null && <ResolveDialog effect={resolving} onClose={() => setResolving(null)} />}
    </section>
  )
}

// ChangePage shows one change: its state and error, a composite's steps,
// and its effects, each unresolved one with a read-only preview before it
// can be resolved.
export function ChangePage() {
  const { changeId = "" } = useParams()
  const change = useQuery({ queryKey: changeQueryKey(changeId), queryFn: () => getChange(changeId) })

  if (change.isPending) return <Spinner aria-label="Loading the change" />
  if (change.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(change.error)}</AlertDescription>
      </Alert>
    )
  }
  const c = change.data
  return (
    <>
      <PageHeader
        breadcrumbs={[{ label: "Audit log", href: "/audit" }, { label: c.id }]}
        title={`${c.action}/v${c.version}`}
        description={`Change ${c.id}`}
      />
      <div className="flex flex-col gap-6">
        <Summary change={c} />
        <ChangeActions key={c.id} change={c} />
        {c.class === "composite" && <Steps changeId={c.id} />}
        <Effects change={c} />
      </div>
    </>
  )
}
