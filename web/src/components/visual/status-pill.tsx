import type { LucideIcon } from "lucide-react"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { statusIcon, statusText, type Status } from "./status"

const variant: Record<Status, BadgeVariant> = {
  ok: "success-subtle",
  warn: "warning-subtle",
  error: "destructive-subtle",
  info: "info-subtle",
  off: "outline",
}

export function StatusPill({ status, label, icon, live }: { status: Status; label: string; icon?: LucideIcon; live?: boolean }) {
  const Icon = icon ?? statusIcon[status]
  // On glass in dark mode the subtle badges' status hues fall under 4.5:1 as
  // text, so the label takes the foreground and the icon (a graphic, 3:1)
  // carries the status colour.
  const pill = (
    <Badge variant={variant[status]} data-status={status} className="text-foreground gap-1">
      <Icon aria-hidden className={`size-3.5 ${statusText[status]}`} />
      {label}
    </Badge>
  )
  if (!live) return pill
  return (
    <div role="status" aria-live="polite" className="inline-flex">
      {pill}
    </div>
  )
}
