import { Badge } from "darkraise-ui/components/badge"
import { cn } from "darkraise-ui/lib"

export function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <Badge variant="outline" aria-label={`${label}: ${value}`} className="max-w-full gap-1.5 whitespace-normal">
      <span className="text-muted-foreground shrink-0">{label}</span>
      <span className={cn("min-w-0 break-all", mono && "font-mono")}>{value}</span>
    </Badge>
  )
}
