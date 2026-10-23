import { useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Textarea } from "darkraise-ui/components/textarea"
import {
  changeQueryKey,
  effectsQueryKey,
  errorMessage,
  listSteps,
  releaseChange,
  repairChange,
  stepsQueryKey,
  undoChange,
} from "@/api/client"
import type { Change, Plan } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { undoOffered } from "@/lib/undo"

const MAX_RELEASE_REASON = 500

function UndoDialog({ change, onClose }: { change: Change; onClose: () => void }) {
  const client = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function undo() {
    setBusy(true)
    setError(null)
    try {
      const next = await undoChange(change.id)
      client.setQueryData(changeQueryKey(change.id), next)
      void client.invalidateQueries({ queryKey: effectsQueryKey(change.id) })
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(next) => (next || busy ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Undo this change?</DialogTitle>
          <DialogDescription>
            Darkpanel runs the change's inverse. It refuses when a later change has taken the same locks.
          </DialogDescription>
        </DialogHeader>
        {error !== null && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            Cancel
          </Button>
          <Button loading={busy} onClick={() => void undo()}>
            Undo
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ReleaseDialog({ change, onClose, onDone }: { change: Change; onClose: () => void; onDone: (message: string) => void }) {
  const client = useQueryClient()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const reason = String(new FormData(event.currentTarget).get("reason") ?? "").trim()
    if (reason === "") {
      setError("Enter why the change is given up.")
      return
    }
    setBusy(true)
    setError(null)
    try {
      const res = await releaseChange(change.id, reason)
      client.setQueryData(changeQueryKey(change.id), res.change)
      void client.invalidateQueries({ queryKey: effectsQueryKey(change.id) })
      const n = res.abandoned.length
      onDone(`Released. ${n} ${n === 1 ? "effect" : "effects"} abandoned.`)
      onClose()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(next) => (next || busy ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Release this change?</DialogTitle>
          <DialogDescription>
            Releasing gives the change up: its locks are freed and every effect it could not settle is abandoned as it is.
          </DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
          <div className="flex flex-col gap-2">
            <Label htmlFor="release-reason">Reason</Label>
            <Textarea id="release-reason" name="reason" required maxLength={MAX_RELEASE_REASON} rows={3} />
          </div>
          {error !== null && (
            <Alert variant="destructive" role="alert">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button type="submit" variant="destructive" loading={busy}>
              Release
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// StepDecision repairs a composite by deciding one step: the operator says
// whether its work counts as reverted or as applied. Only the step at the
// composite's cursor can be decided, which the engine checks.
function StepDecision({ change, onError }: { change: Change; onError: (m: string | null) => void }) {
  const client = useQueryClient()
  const steps = useQuery({ queryKey: stepsQueryKey(change.id), queryFn: () => listSteps(change.id) })
  const [step, setStep] = useState("")
  const [as, setAs] = useState<"reverted" | "applied">("reverted")
  const [busy, setBusy] = useState(false)

  async function decide() {
    setBusy(true)
    onError(null)
    try {
      const res = await repairChange(change.id, { step, as })
      if (res.change !== undefined) client.setQueryData(changeQueryKey(res.change.id), res.change)
      void client.invalidateQueries({ queryKey: changeQueryKey(change.id) })
      void client.invalidateQueries({ queryKey: stepsQueryKey(change.id) })
    } catch (err) {
      onError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (steps.data === undefined || steps.data.length === 0) return null
  return (
    <fieldset className="flex flex-col gap-3 rounded-md border p-3">
      <legend className="px-1 text-sm font-medium">Decide a step</legend>
      <RadioGroup value={step} onValueChange={setStep} aria-label="Step">
        {[...steps.data].sort((a, b) => a.ordinal - b.ordinal).map((s) => (
          <div key={s.id} className="flex items-center gap-2">
            <RadioGroupItem id={`decide-step-${s.id}`} value={s.id} />
            <Label htmlFor={`decide-step-${s.id}`}>{`${s.id} (${s.state})`}</Label>
          </div>
        ))}
      </RadioGroup>
      <RadioGroup value={as} onValueChange={(v) => setAs(v as "reverted" | "applied")} aria-label="Count it as">
        {(["reverted", "applied"] as const).map((v) => (
          <div key={v} className="flex items-center gap-2">
            <RadioGroupItem id={`decide-as-${v}`} value={v} />
            <Label htmlFor={`decide-as-${v}`}>{v === "reverted" ? "Reverted" : "Applied"}</Label>
          </div>
        ))}
      </RadioGroup>
      <Button className="self-start" variant="outline" disabled={step === ""} loading={busy} onClick={() => void decide()}>
        Decide step
      </Button>
    </fieldset>
  )
}

// ChangeActions offers what can be done to a change: Undo on an undoable
// one, and Repair, Release and, for a composite, a step decision on one
// that needs repair.
export function ChangeActions({ change }: { change: Change }) {
  const client = useQueryClient()
  const [dialog, setDialog] = useState<"undo" | "release" | null>(null)
  const [repairPlan, setRepairPlan] = useState<Plan | null>(null)
  const [repairing, setRepairing] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const needsRepair = change.state === "needs_repair"

  async function repair() {
    setRepairing(true)
    setError(null)
    setNotice(null)
    try {
      const res = await repairChange(change.id, {})
      if (res.plan !== undefined) setRepairPlan(res.plan)
      if (res.change !== undefined) client.setQueryData(changeQueryKey(res.change.id), res.change)
      void client.invalidateQueries({ queryKey: changeQueryKey(change.id) })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setRepairing(false)
    }
  }

  // A release moves the change out of needs_repair; the report of what it
  // abandoned must outlive that.
  if (!undoOffered(change) && !needsRepair && notice === null) return null
  return (
    <section aria-label="Actions" className="flex flex-col gap-3">
      <div className="flex flex-wrap gap-2">
        {undoOffered(change) && (
          <Button variant="outline" onClick={() => setDialog("undo")}>
            Undo
          </Button>
        )}
        {needsRepair && (
          <>
            <Button loading={repairing} onClick={() => void repair()}>
              Repair
            </Button>
            <Button variant="destructive" onClick={() => setDialog("release")}>
              Release
            </Button>
          </>
        )}
      </div>
      {needsRepair && change.class === "composite" && <StepDecision change={change} onError={setError} />}
      {error !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      {notice !== null && <p role="status" className="text-sm">{notice}</p>}
      {dialog === "undo" && <UndoDialog change={change} onClose={() => setDialog(null)} />}
      {dialog === "release" && <ReleaseDialog change={change} onClose={() => setDialog(null)} onDone={setNotice} />}
      {repairPlan !== null && (
        <ChangeDialog
          open
          onOpenChange={(next) => {
            if (!next) setRepairPlan(null)
          }}
          title="Repair the change"
          target={{ name: repairPlan.action, version: repairPlan.version, hostId: repairPlan.hostId ?? undefined }}
          plan={repairPlan}
          onApplied={() => void client.invalidateQueries({ queryKey: changeQueryKey(change.id) })}
        />
      )}
    </section>
  )
}
