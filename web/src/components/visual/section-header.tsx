import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"
import { StatusPill } from "./status-pill"
import type { Status } from "./status"

export function SectionHeader({
  icon: Icon,
  title,
  status,
  actions,
  level = 2,
}: {
  icon?: LucideIcon
  title: string
  status?: { status: Status; label: string }
  actions?: ReactNode
  level?: 1 | 2
}) {
  const Heading = level === 1 ? "h1" : "h2"
  return (
    <div className="flex flex-wrap items-center gap-2">
      {Icon && <Icon aria-hidden className="text-muted-foreground size-5" />}
      <Heading className="min-w-0 text-lg font-semibold wrap-anywhere">{title}</Heading>
      {status && <StatusPill status={status.status} label={status.label} />}
      {actions && <div className="ms-auto flex flex-wrap items-center gap-2">{actions}</div>}
    </div>
  )
}
