import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { FIREWALL_ACTION_VERSION, FIREWALL_MODULE } from "./types"

export interface FirewallChangeProps {
  hostId: string
  action: string // a mutating firewall action, such as "firewall.rule.add"
  title: string
  input: Record<string, unknown> | null // null keeps the dialog closed
  onClose: () => void
}

// FirewallChange plans and applies one firewall action through the change
// dialog. Every mutating firewall action is revertible, so the dialog asks
// for the revert timeout. The host's resources are read again once the
// change applies and when the dialog closes, since a revert by the timer
// raises no event that names them.
export function FirewallChange({ hostId, action, title, input, onClose }: FirewallChangeProps) {
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
      target={{ name: action, version: FIREWALL_ACTION_VERSION, hostId, module: FIREWALL_MODULE, revertible: true }}
      input={input ?? undefined}
      onApplied={refresh}
    />
  )
}
