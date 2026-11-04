import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { PANEL_ACTION_VERSION, PANEL_MODULE } from "./types"

export interface PanelChangeProps {
  hostId: string // the host whose page opened the dialog
  action: string // a composite action, such as "panel.exposure.add"
  title: string
  input: Record<string, unknown> | null // null keeps the dialog closed
  onClose: () => void
}

// PanelChange plans and applies one panel composite on the controller. On
// close it refetches the panel reads the banner and the page use, and the
// resources of the page's host, whose NPM, tunnel and firewall state the
// composite changes.
export function PanelChange({ hostId, action, title, input, onClose }: PanelChangeProps) {
  const client = useQueryClient()
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={(open) => {
        if (open) return
        onClose()
        void client.invalidateQueries({ queryKey: [PANEL_MODULE] })
        void client.invalidateQueries({ queryKey: ["resource", hostId] })
      }}
      title={title}
      target={{ name: action, version: PANEL_ACTION_VERSION }}
      input={input ?? undefined}
    />
  )
}
