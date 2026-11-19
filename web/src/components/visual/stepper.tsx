import { cn } from "darkraise-ui/lib"
import { StatusDot } from "./status-dot"
import { statusText, type Status } from "./status"

const stateLabel: Record<Status, string> = { ok: "Done", info: "In progress", error: "Failed", off: "Not started", warn: "Warning" }

// done marks every step finished, past the last one.
export function Stepper({ steps, current, failed, done }: { steps: { id: string; label: string }[]; current: string; failed?: boolean; done?: boolean }) {
  const at = done ? steps.length : steps.findIndex((s) => s.id === current)
  return (
    <ol aria-label="Progress" className="flex flex-wrap items-center gap-x-4 gap-y-2 text-sm">
      {steps.map((s, i) => {
        const status: Status = i < at ? "ok" : i === at ? (failed ? "error" : "info") : "off"
        return (
          <li key={s.id} data-status={status} aria-current={i === at ? "step" : undefined} className="flex items-center gap-2">
            <StatusDot status={status} label={stateLabel[status]} />
            <span className={cn(i === at ? "font-semibold" : status === "off" && "text-muted-foreground", status === "error" && statusText.error)}>{s.label}</span>
          </li>
        )
      })}
    </ol>
  )
}
