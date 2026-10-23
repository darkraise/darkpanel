import type { Change } from "@/api/types"

const UNDOABLE_STATES = new Set(["completed", "failed", "interrupted", "needs_repair"])

// undoOffered mirrors the engine's undo admission as far as a ChangeView
// shows it: a compensable change that nothing owns, not a composite's step
// unless the step itself needs repair, in a state undo starts from. The
// engine still refuses one whose action has no undo handler, whose window
// a later change closed, or that has an unresolved effect; the page shows
// that refusal.
export function undoOffered(change: Change): boolean {
  return (
    change.class === "compensable" &&
    change.owner === null &&
    (change.parentChangeId === null || change.state === "needs_repair") &&
    UNDOABLE_STATES.has(change.state)
  )
}
