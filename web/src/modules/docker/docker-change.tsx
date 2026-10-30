import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { DOCKER_ACTION_VERSION, DOCKER_MODULE } from "./types"

export interface DockerChangeProps {
  hostId: string
  action: string // a mutating docker action, such as "docker.network.remove"
  title: string
  input: Record<string, unknown> | null // null keeps the dialog closed
  onClose: () => void
}

// DockerChange plans and applies one Docker action through the change
// dialog. A Docker change is not reverted by a timer, so no revert timeout is
// asked. The host's resources are read again when the dialog closes.
export function DockerChange({ hostId, action, title, input, onClose }: DockerChangeProps) {
  const client = useQueryClient()
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={(open) => {
        if (open) return
        onClose()
        void client.invalidateQueries({ queryKey: ["resource", hostId] })
      }}
      title={title}
      target={{ name: action, version: DOCKER_ACTION_VERSION, hostId, module: DOCKER_MODULE }}
      input={input ?? undefined}
    />
  )
}
