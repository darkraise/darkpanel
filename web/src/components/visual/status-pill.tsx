import type { LucideIcon } from "lucide-react"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { statusIcon, type Status } from "./status"

const variant: Record<Status, BadgeVariant> = {
  ok: "success-subtle",
  warn: "warning-subtle",
  error: "destructive-subtle",
  info: "info-subtle",
  off: "secondary",
}

export function StatusPill({ status, label, icon, live }: { status: Status; label: string; icon?: LucideIcon; live?: boolean }) {
  const Icon = icon ?? statusIcon[status]
  const pill = (
    <Badge variant={variant[status]} data-status={status} className="gap-1">
      <Icon aria-hidden className="size-3.5" />
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
