import { Badge } from "darkraise-ui/components/badge"
import { cn } from "darkraise-ui/lib"

// A secret value is read from its text alone: an aria-label would copy it
// into an attribute that accessibility snapshots and traces record.
export function Fact({ label, value, mono, secret }: { label: string; value: string; mono?: boolean; secret?: boolean }) {
  return (
    <Badge variant="outline" aria-label={secret ? undefined : `${label}: ${value}`} className="max-w-full gap-1.5 whitespace-normal">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className={cn("min-w-0 break-all", mono && "font-mono")}>{value}</span>
    </Badge>
  )
}
