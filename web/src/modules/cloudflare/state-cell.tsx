import { StatusDot } from "@/components/visual/status-dot"
import type { StateView } from "./types"

// The dot names the state for assistive technology, so the visible label is hidden from it.
export function StateCell({ state }: { state: StateView }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <StatusDot status={state.status} label={state.label} />
      <span aria-hidden>{state.label}</span>
    </span>
  )
}
