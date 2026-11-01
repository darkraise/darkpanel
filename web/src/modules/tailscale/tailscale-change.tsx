import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { TAILSCALE_ACTION_VERSION, TAILSCALE_MODULE } from "./types"

export interface TailscaleChangeProps {
  hostId: string
  action: string // a mutating tailscale action, such as "tailscale.prefs.set"
  title: string
  input: Record<string, unknown> | null // null keeps the dialog closed
  onClose: () => void
}

const PREFS_SET = "tailscale.prefs.set"

// TailscaleChange plans and applies one tailscale action through the change
// dialog. Only tailscale.prefs.set is reverted by a timer, so only it asks
// for the revert timeout. The host's resources are read again once the
// change applies and when the dialog closes, since a revert by the timer
// raises no event that names them.
export function TailscaleChange({ hostId, action, title, input, onClose }: TailscaleChangeProps) {
  const client = useQueryClient()
  const refresh = () => void client.invalidateQueries({ queryKey: ["resource", hostId] })
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={(open) => {
        if (open) return
        onClose()
        refresh()
      }}
      title={title}
      target={{ name: action, version: TAILSCALE_ACTION_VERSION, hostId, module: TAILSCALE_MODULE, revertible: action === PREFS_SET }}
      input={input ?? undefined}
      onApplied={refresh}
    />
  )
}
