import { useId, type ReactNode } from "react"
import type { LucideIcon } from "lucide-react"
import { RotateCcw } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader } from "darkraise-ui/components/card"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { errorMessage } from "@/api/client"
import { StatusPill } from "@/components/visual/status-pill"
import type { Status } from "@/components/visual/status"

// Tile is one System overview tile: a region named by its title, a status
// pill, a body and a row of actions. A greyed tile belongs to an offline host.
export function Tile({
  icon: Icon,
  title,
  status,
  greyed,
  loading,
  actions,
  children,
}: {
  icon: LucideIcon
  title: string
  status?: { status: Status; label: string; live?: boolean }
  greyed?: boolean
  loading?: boolean // a busy card, named as a region once its content is there
  actions?: ReactNode
  children?: ReactNode
}) {
  const id = useId()
  return (
    <Card
      role={loading ? undefined : "region"}
      aria-labelledby={loading ? undefined : id}
      aria-busy={loading || undefined}
      className={greyed ? "bg-muted/40" : undefined}
    >
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0 p-4 pb-2">
        <h3 id={id} className="flex items-center gap-2 text-sm font-medium">
          <Icon aria-hidden className="text-muted-foreground size-4" />
          {title}
        </h3>
        {status && <StatusPill status={status.status} label={status.label} live={status.live} />}
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-3 p-4 pt-0">
        {children}
        {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
      </CardContent>
    </Card>
  )
}

// TileSkeleton fills a loading tile's body at the height of its content.
export function TileSkeleton() {
  return (
    <div className="flex flex-col gap-3" aria-hidden>
      <Skeleton className="h-8 w-24" />
      <Skeleton className="h-4 w-full" />
      <Skeleton className="h-8 w-32" />
    </div>
  )
}

// TileError is a failed read: the error and a retry, inside its own tile.
export function TileError({ error, onRetry, disabled }: { error: unknown; onRetry: () => void; disabled?: boolean }) {
  return (
    <Alert variant="destructive" role="alert">
      <AlertDescription className="flex flex-col items-start gap-2">
        <span className="break-words">{errorMessage(error)}</span>
        <Button size="sm" variant="outline" disabled={disabled} onClick={onRetry}>
          <RotateCcw aria-hidden />
          Retry
        </Button>
      </AlertDescription>
    </Alert>
  )
}
