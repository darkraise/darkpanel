import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"
import { StatusPill } from "./status-pill"
import type { Status } from "./status"

export function SectionHeader({
  icon: Icon,
  title,
  status,
  actions,
}: {
  icon?: LucideIcon
  title: string
  status?: { status: Status; label: string }
  actions?: ReactNode
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      {Icon && <Icon aria-hidden className="text-muted-foreground size-5" />}
      <h2 className="text-lg font-semibold">{title}</h2>
      {status && <StatusPill status={status.status} label={status.label} />}
      {actions && <div className="ms-auto flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
