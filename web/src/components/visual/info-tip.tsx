import { useState, type ReactNode } from "react"
import { Info } from "lucide-react"
import { Button } from "darkraise-ui/components/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "darkraise-ui/components/tooltip"

// The kit tooltip opens on hover and keyboard focus only; a tap toggles it too,
// so touch users can read what the copy budget moved into the tip.
export function InfoTip({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false)
  return (
    <Tooltip open={open} onOpenChange={setOpen}>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label={label}
          className="text-muted-foreground size-7"
          onPointerDown={(e) => e.preventDefault()}
          onClick={() => setOpen((o) => !o)}
        >
          <Info aria-hidden className="size-4" />
        </Button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{children}</TooltipContent>
    </Tooltip>
  )
}
