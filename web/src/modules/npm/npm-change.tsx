import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import type { Change } from "@/api/types"
import { NPM_ACTION_VERSION, NPM_MODULE, npmFleetKey } from "./types"

export interface NpmChangeProps {
  hostId: string
  action: string // an agent action, such as "npm.proxyhost.add"
  title: string
  input: Record<string, unknown> | null // null keeps the dialog closed
  onClose: () => void
  onApplied?: (change: Change) => void
}

// NpmChange plans and applies one agent action on a host. On close it
// refetches that host's resources, which the NPM page reads under
// ["resource", hostId, ...], and the fleet read the banner uses.
export function NpmChange({ hostId, action, title, input, onClose, onApplied }: NpmChangeProps) {
  const client = useQueryClient()
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={(open) => {
        if (open) return
        onClose()
        void client.invalidateQueries({ queryKey: ["resource", hostId] })
        void client.invalidateQueries({ queryKey: npmFleetKey })
      }}
      title={title}
      target={{ name: action, version: NPM_ACTION_VERSION, hostId, module: NPM_MODULE }}
      input={input ?? undefined}
      onApplied={onApplied}
    />
  )
}

export interface NpmControllerChangeProps {
  action: string // a composite action, such as "npm.expose.container"
  title: string
  input: Record<string, unknown> | null // null keeps the dialog closed
  onClose: () => void
}

// NpmControllerChange plans and applies one composite action on the
// controller. On close it refetches the fleet read the banner uses and, when
// the input names a host, that host's resources, because a composite changes
// what that host's NPM page reads.
export function NpmControllerChange({ action, title, input, onClose }: NpmControllerChangeProps) {
  const client = useQueryClient()
  const hostId = input !== null && typeof input.hostId === "string" ? input.hostId : null
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={(open) => {
        if (open) return
        onClose()
        void client.invalidateQueries({ queryKey: [NPM_MODULE] })
        if (hostId !== null) void client.invalidateQueries({ queryKey: ["resource", hostId] })
      }}
      title={title}
      target={{ name: action, version: NPM_ACTION_VERSION }}
      input={input ?? undefined}
    />
  )
}
