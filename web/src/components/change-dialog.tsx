import { useEffect, useState } from "react"
import { Link } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import {
  applyPlan,
  changeError,
  changeQueryKey,
  confirmChange,
  errorMessage,
  getAvailability,
  getChange,
  listSteps,
  planAction,
  revertChange,
  stepsQueryKey,
} from "@/api/client"
import type { Change, Guard, Plan } from "@/api/types"

export interface ChangeTarget {
  name: string
  version: number
  hostId?: string
  module?: string // checked against getAvailability when hostId is set
}

export interface ChangeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  target: ChangeTarget
  input?: Record<string, unknown> // planned on open; the dialog adds "overrides"
  plan?: Plan // a plan already made (a repair's answer): shown and applied as is
  onApplied?: (change: Change) => void
}

function stateVariant(state: string): BadgeVariant {
  switch (state) {
    case "completed":
    case "confirmed":
      return "default"
    case "failed":
    case "needs_repair":
    case "interrupted":
      return "destructive"
    default:
      return "secondary"
  }
}

// secondsLeft is the whole seconds until deadline, never below zero.
function secondsLeft(deadline: string, now: number): number {
  return Math.max(0, Math.ceil((Date.parse(deadline) - now) / 1000))
}

function Countdown({ deadline }: { deadline: string }) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [])
  return <p role="timer" aria-live="off">{`Reverts automatically in ${secondsLeft(deadline, now)} s unless confirmed.`}</p>
}

function GuardList({ guards, overrides, canOverride, onToggle }: {
  guards: Guard[]
  overrides: string[]
  canOverride: boolean
  onToggle: (id: string, on: boolean) => void
}) {
  if (guards.length === 0) return null
  return (
    <ul aria-label="Guards" className="flex flex-col gap-2">
      {guards.map((g) => (
        <li key={g.id} className="flex flex-col gap-1">
          <div className="flex items-center gap-2">
            <Badge variant={g.ok ? (g.overridden ? "secondary" : "default") : "destructive"} size="sm">
              {g.overridden ? "overridden" : g.ok ? "passed" : "failed"}
            </Badge>
            <span className="font-mono text-sm">{g.id}</span>
          </div>
          <p className="text-sm text-muted-foreground">{g.message}</p>
          {canOverride && g.overridable && (!g.ok || g.overridden) && (
            <div className="flex items-center gap-2">
              <Checkbox
                id={`override-${g.id}`}
                checked={overrides.includes(g.id)}
                onCheckedChange={(checked) => onToggle(g.id, checked === true)}
              />
              <Label htmlFor={`override-${g.id}`}>{`Override ${g.id}`}</Label>
            </div>
          )}
        </li>
      ))}
    </ul>
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
    <ol aria-label="Steps" className="flex flex-col gap-1 text-sm">
      {[...steps.data].sort((a, b) => a.ordinal - b.ordinal).map((s) => (
        <li key={s.id} className="flex flex-wrap items-center gap-2">
          <span className="font-mono">{`${s.id} (${s.action})`}</span>
          <Badge variant="outline" size="sm">{s.state}</Badge>
          {s.error !== null && <span className="text-destructive">{s.error.message}</span>}
        </li>
      ))}
    </ol>
  )
}

// Applied shows a change after its apply and follows it: the event stream
// invalidates its query, so a confirmation, a revert by the timer or a
// composite's progress shows without a reload.
function Applied({ applied, onError }: { applied: Change; onError: (message: string | null) => void }) {
  const change = useQuery({ queryKey: changeQueryKey(applied.id), queryFn: () => getChange(applied.id), initialData: applied })
  const [busy, setBusy] = useState(false)
  const current = change.data

  async function decide(confirm: boolean) {
    setBusy(true)
    onError(null)
    try {
      const next = await (confirm ? confirmChange(current.id) : revertChange(current.id))
      change.refetch().catch(() => {})
      onError(changeError(next))
    } catch (err) {
      onError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const failure = changeError(current)
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className="text-sm">State</span>
        <Badge variant={stateVariant(current.state)}>{current.state}</Badge>
      </div>
      {failure !== null && current.state !== "applying" && current.state !== "awaiting_confirmation" && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
      {current.state === "pending" && current.revertDeadline !== null && (
        <div className="flex flex-col gap-2">
          <Countdown deadline={current.revertDeadline} />
          <div className="flex gap-2">
            <Button loading={busy} onClick={() => void decide(true)}>
              Confirm
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => void decide(false)}>
              Revert
            </Button>
          </div>
        </div>
      )}
      {current.class === "composite" && <Steps changeId={current.id} />}
      <Link to={`/changes/${encodeURIComponent(current.id)}`} className="text-sm underline-offset-4 hover:underline">
        View change
      </Link>
    </div>
  )
}

// planQueryKey identifies one plan request. connectEvents never refetches a
// query under "plan", because refetching it would post a new plan.
function planQueryKey(target: ChangeTarget, input: string, overrides: string[]) {
  return ["plan", target.name, target.version, target.hostId ?? null, input, overrides] as const
}

interface BodyProps extends Omit<ChangeDialogProps, "open" | "onOpenChange" | "title"> {
  busy: boolean
  setBusy: (busy: boolean) => void
  close: () => void
}

// ChangeDialogBody holds one opening's state; it mounts when the dialog
// opens, so closing forgets the plan, the overrides and the outcome.
function ChangeDialogBody({ target, input, plan: given, onApplied, busy, setBusy, close }: BodyProps) {
  const [overrides, setOverrides] = useState<string[]>([])
  const [typed, setTyped] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [applied, setApplied] = useState<Change | null>(null)
  // The overrides are part of the plan's query key, so a toggle starts a
  // query with no data. Showing the last answered plan meanwhile keeps the
  // override checkbox mounted and focused, and lets it be unticked when the
  // replan is refused.
  const [lastPlan, setLastPlan] = useState<Plan | null>(null)
  const inputKey = JSON.stringify(input ?? {})

  const planned = useQuery({
    queryKey: planQueryKey(target, inputKey, overrides),
    queryFn: async () => {
      const body = JSON.parse(inputKey) as Record<string, unknown>
      if (overrides.length > 0) body.overrides = overrides
      const next = await planAction(target.name, target.version, body, target.hostId)
      setLastPlan(next)
      return next
    },
    enabled: given === undefined,
    staleTime: Infinity,
    gcTime: 0,
    retry: false,
    refetchOnWindowFocus: false,
  })
  const availability = useQuery({
    queryKey: ["availability", target.hostId, target.module],
    queryFn: () => getAvailability(target.hostId ?? "", target.module ?? ""),
    enabled: target.hostId !== undefined && target.module !== undefined,
  })

  const plan = given ?? planned.data ?? lastPlan
  const planError = given === undefined && planned.isError ? errorMessage(planned.error) : null

  function toggleOverride(id: string, on: boolean) {
    setOverrides((cur) => (on ? [...cur.filter((x) => x !== id), id] : cur.filter((x) => x !== id)))
  }

  let unavailable: string | null = null
  if (availability.data !== undefined) {
    const action = availability.data.actions.find((a) => a.name === target.name)
    if (!availability.data.available) unavailable = availability.data.reason ?? "The module is unavailable on this host."
    else if (action !== undefined && !action.available) unavailable = action.reason ?? "The action is unavailable on this host."
  }
  const guardsOk = plan !== null && plan.guards.every((g) => g.ok)
  const confirmed = plan === null || plan.confirmation === null || typed === plan.confirmation
  // Only a plan answered for the overrides now selected may be applied, never the last plan kept on screen.
  const answered = given !== undefined || planned.data !== undefined
  const canApply = plan !== null && answered && !planned.isFetching && guardsOk && confirmed && unavailable === null && !busy

  async function apply() {
    if (plan === null || !canApply) return
    setBusy(true)
    setError(null)
    try {
      const change = await applyPlan(plan, plan.confirmation === null ? undefined : typed)
      setApplied(change)
      onApplied?.(change)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  const shownError = error ?? planError
  return (
    <>
      {unavailable !== null && (
        <Alert variant="warning" role="status">
          <AlertTitle>Unavailable</AlertTitle>
          <AlertDescription>{unavailable}</AlertDescription>
        </Alert>
      )}
      {applied !== null ? (
        <Applied applied={applied} onError={setError} />
      ) : plan === null ? (
        shownError === null && <Spinner aria-label="Planning the change" />
      ) : (
        <div className="flex flex-col gap-4">
          <pre aria-label="Planned changes" className="overflow-x-auto rounded-md bg-muted p-3 font-mono text-xs">
            {plan.diff.map((d, i) => (
              <div key={i} className={d.op === "+" ? "text-success" : d.op === "-" ? "text-destructive" : undefined}>
                {`${d.op} ${d.text}`}
              </div>
            ))}
          </pre>
          <GuardList guards={plan.guards} overrides={overrides} canOverride={given === undefined} onToggle={toggleOverride} />
          {plan.confirmation !== null && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="change-confirmation">{`Type ${plan.confirmation} to confirm`}</Label>
              <Input id="change-confirmation" autoComplete="off" value={typed} onChange={(e) => setTyped(e.target.value)} />
            </div>
          )}
        </div>
      )}
      {shownError !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{shownError}</AlertDescription>
        </Alert>
      )}
      <DialogFooter>
        {applied === null ? (
          <Button loading={busy} disabled={!canApply} onClick={() => void apply()}>
            Apply
          </Button>
        ) : (
          <Button variant="outline" onClick={close}>
            Close
          </Button>
        )}
      </DialogFooter>
    </>
  )
}

// ChangeDialog plans one action, shows what it will do and the guards it
// ran, and applies it. A failed guard the action marks overridable can be
// overridden, which plans again with the guard listed in "overrides".
export function ChangeDialog({ open, onOpenChange, title, ...body }: ChangeDialogProps) {
  const [busy, setBusy] = useState(false)

  // While an apply is in flight the dialog stays open, so its outcome
  // cannot be lost.
  function changeOpen(next: boolean) {
    if (!next && busy) return
    onOpenChange(next)
  }

  const where = body.target.hostId === undefined ? "on the controller" : `on host ${body.target.hostId}`
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{`${body.target.name}/v${body.target.version} ${where}`}</DialogDescription>
        </DialogHeader>
        {open && <ChangeDialogBody {...body} busy={busy} setBusy={setBusy} close={() => changeOpen(false)} />}
      </DialogContent>
    </Dialog>
  )
}
