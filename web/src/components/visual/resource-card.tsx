import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"
import { Card, CardContent } from "darkraise-ui/components/card"
import { StatusPill } from "./status-pill"
import type { Status } from "./status"

export function ResourceCard({
  icon: Icon,
  title,
  status,
  figures,
  actions,
  children,
}: {
  icon: LucideIcon
  title: string
  status: { status: Status; label: string }
  figures?: ReactNode
  actions?: ReactNode
  children?: ReactNode
}) {
  return (
    <Card role="article" aria-label={title}>
      <CardContent className="flex flex-col gap-3 p-4">
        <div className="flex items-start gap-3">
          <span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-md">
            <Icon aria-hidden className="size-5" />
          </span>
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="truncate font-semibold">{title}</span>
            <div>
              <StatusPill status={status.status} label={status.label} />
            </div>
          </div>
          {actions && <div className="flex shrink-0 items-center gap-1">{actions}</div>}
        </div>
        {figures && <div className="flex flex-col gap-2">{figures}</div>}
        {children}
      </CardContent>
    </Card>
  )
}
