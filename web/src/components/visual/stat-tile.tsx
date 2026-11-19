import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"
import { Card, CardContent } from "darkraise-ui/components/card"

export function StatTile({ icon: Icon, figure, caption, children }: { icon: LucideIcon; figure: string | number; caption: string; children?: ReactNode }) {
  return (
    <Card role="group" aria-label={`${caption}: ${figure}`}>
      <CardContent className="flex items-start gap-3 p-4">
        <span className="bg-muted text-muted-foreground flex size-9 shrink-0 items-center justify-center rounded-md">
          <Icon aria-hidden className="size-5" />
        </span>
        <div className="min-w-0">
          <div className="font-mono text-2xl font-semibold tabular-nums">{figure}</div>
          <div className="text-muted-foreground text-sm">{caption}</div>
          {children}
        </div>
      </CardContent>
    </Card>
  )
}
