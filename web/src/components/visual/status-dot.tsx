import { cn } from "darkraise-ui/lib"
import { statusIcon, statusText, type Status } from "./status"

export function StatusDot({ status, label, className }: { status: Status; label: string; className?: string }) {
  const Icon = statusIcon[status]
  return (
    <span role="img" aria-label={label} data-status={status} className={cn("inline-flex shrink-0", statusText[status], className)}>
      <Icon aria-hidden className="size-4" />
    </span>
  )
}
