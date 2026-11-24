import { useState } from "react"
import { Link, useParams } from "react-router"
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Minus, Pencil, Plus, type LucideIcon } from "lucide-react"
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
import type { Change, Effect, EffectKind, StateDoc } from "@/api/types"
import { Fact } from "@/components/visual/fact"
import { StatusDot } from "@/components/visual/status-dot"
import { StatusPill } from "@/components/visual/status-pill"
import { Stepper } from "@/components/visual/stepper"
import { statusText, type Status } from "@/components/visual/status"
import { formatTime } from "@/lib/format"
import { usePageTitle } from "@/lib/page-title"
import { isAdmin } from "@/session/can"
import { useSession } from "@/session/use-session"
import { ChangeActions } from "./change-actions"

// stateStatus covers the change, step and effect states.
function stateStatus(state: string): Status {
  switch (state) {
    case "completed":
    case "confirmed":
    case "repaired":
    case "kept":
    case "applied":
    case "read":
    case "made":
    case "undone":
    case "accepted":
      return "ok"
    case "failed":
    case "needs_repair":
    case "interrupted":
    case "quarantined":
    case "unresolved":
      return "error"
    case "reverted":
    case "abandoned":
    case "pending":
    case "skipped":
    case "released":
      return "off"
    default:
      return "info"
  }
}

function stateLabel(state: string): string {
  const words = state.replace(/_/g, " ")
  return words.charAt(0).toUpperCase() + words.slice(1)
}

function StateText({ state, text = stateLabel(state) }: { state: string; text?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <StatusDot status={stateStatus(state)} label={text} />
      <span aria-hidden>{text}</span>
    </span>
  )
}

const KIND: Record<EffectKind, { icon: LucideIcon; label: string; status: Status }> = {
  create: { icon: Plus, label: "Create", status: "ok" },
  modify: { icon: Pencil, label: "Modify", status: "warn" },
  delete: { icon: Minus, label: "Delete", status: "error" },
}

const STEPS = [
  { id: "plan", label: "Plan" },
  { id: "confirm", label: "Confirm" },
  { id: "apply", label: "Apply" },
]

// A stored change was planned and confirmed already; it settles once its
// state is final, failed when the apply did not hold.
function ChangeProgress({ state }: { state: string }) {
  const status = stateStatus(state)
  const failed = status === "error" || state === "reverted" || state === "abandoned"
  return <Stepper steps={STEPS} current="apply" failed={failed} done={status === "ok"} />
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

function LinkFact({ label, id }: { label: string; id: string }) {
  return (
    <Badge variant="outline" className="max-w-full gap-1.5 whitespace-normal">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className="min-w-0 break-all">
        <ChangeLink id={id} />
      </span>
    </Badge>
  )
}

function Summary({ change }: { change: Change }) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
        <StatusPill status={stateStatus(change.state)} label={stateLabel(change.state)} live />
        <ChangeProgress state={change.state} />
      </div>
      <div className="flex flex-wrap gap-1">
        <Fact label="Class" value={change.class} />
        <Fact label="Host" value={change.hostId ?? "The controller"} mono={change.hostId !== null} />
        <Fact label="Actor" value={change.actor} mono />
        <Fact label="Created" value={formatTime(change.createdAt)} />
        <Fact label="Updated" value={formatTime(change.updatedAt)} />
        {change.revertDeadline !== null && <Fact label="Revert deadline" value={formatTime(change.revertDeadline)} />}
        {change.owner !== null && <Fact label="Being worked on by" value={change.owner} />}
        {change.parentChangeId !== null && <LinkFact label="Step of" id={change.parentChangeId} />}
        {change.repairedBy !== null && <LinkFact label="Repaired by" id={change.repairedBy} />}
        {change.repairOutcome !== null && <Fact label="Repair outcome" value={change.repairOutcome} />}
        {change.compensatedBy !== null && <Fact label="Compensated by" value={change.compensatedBy} mono />}
      </div>
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
                <StateText state={s.error !== null ? "failed" : s.state} text={stateLabel(s.state)} />
                {s.error !== null && <p className="text-xs text-destructive-text">{s.error.message}</p>}
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
      // The picked candidate tells reconcile which object to act on; accept
      // takes the resource as it is, so it sends none.
      await resolveEffect(effect.changeId, effect.seq, resolution === "reconcile" && resource !== "" ? { resolution, resource } : { resolution })
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

function EffectKindCell({ kind }: { kind: EffectKind }) {
  const k = KIND[kind]
  return (
    <span className="inline-flex items-center gap-1.5">
      <span role="img" aria-label={k.label} className={`inline-flex shrink-0 ${statusText[k.status]}`}>
        <k.icon aria-hidden className="size-4" />
      </span>
      <span aria-hidden>{k.label}</span>
    </span>
  )
}

function Effects({ change }: { change: Change }) {
  // Change events refetch the change, not its effects; keying the effects
  // on the change's last update refetches them whenever the change moved on.
  // The previous effects stay shown meanwhile, so an open resolve dialog is
  // not unmounted with its pick.
  const effects = useQuery({
    queryKey: [...effectsQueryKey(change.id), change.updatedAt, change.state],
    queryFn: () => listEffects(change.id),
    placeholderData: keepPreviousData,
  })
  const [resolving, setResolving] = useState<Effect | null>(null)
  const admin = isAdmin(useSession().session)

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
                <TableCell>
                  <EffectKindCell kind={e.kind} />
                </TableCell>
                <TableCell className="font-mono">{e.resource}</TableCell>
                <TableCell>
                  <StateText state={e.state} text={e.reason === "" ? stateLabel(e.state) : `${stateLabel(e.state)}: ${e.reason}`} />
                </TableCell>
                <TableCell>
                  {e.state === "unresolved" && admin ? (
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
  usePageTitle(`Change ${changeId.slice(-8)}`)
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
