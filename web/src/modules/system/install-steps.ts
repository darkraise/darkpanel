import type { Step } from "@/api/types"

// The stepper position of each composite step, and the step states that
// finish one (a read step reads; an applied step awaits or needs no
// confirmation; composite steps have no "completed" state).
const STEP_OF: Record<string, string> = {
  "system.docker.check": "check",
  "system.docker.engine-install": "packages",
  "firewall.docker.enable": "firewall",
}
const FINISHED = new Set(["read", "applied", "confirmed"])

// A step's action arrives as "<name>/v<version>".
const stepOf = (s: Step): string | undefined => STEP_OF[s.action.replace(/\/v\d+$/, "")]

// stepAt is the stepper position of an install: the confirmation while the
// firewall step awaits it; for a failed install the step that failed (the
// one carrying the error, else the last one attempted); otherwise the first
// step not yet finished.
export function stepAt(all: { id: string }[], steps: Step[] | undefined, awaiting: boolean, failed = false): string {
  if (awaiting) return "confirm"
  const known = (steps ?? []).filter((s) => stepOf(s) !== undefined).sort((a, b) => a.ordinal - b.ordinal)
  const at = (s: Step | undefined) => (s === undefined ? undefined : stepOf(s))
  if (failed) {
    const attempted = known.filter((s) => s.state !== "pending")
    const failedStep = known.find((s) => s.error !== null) ?? attempted[attempted.length - 1]
    return at(failedStep) ?? all[0]?.id ?? "check"
  }
  const next = at(known.find((s) => !FINISHED.has(s.state)))
  if (next !== undefined) return next
  return all[Math.min(known.length, all.length - 1)]?.id ?? "check"
}
