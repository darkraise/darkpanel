import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { CLOUDFLARE_ACTION_VERSION } from "./types"

const CLOUDFLARE_MODULE = "cloudflare"

export interface CloudflareChangeProps {
  action: string
  title: string
  input: Record<string, unknown> | null
  // Set for an agent action: the host it runs on, whose module availability the dialog checks.
  hostId?: string
  onClose: () => void
}

/**
 * Plans and applies one Cloudflare action, on the controller or on the host
 * `hostId` names, in a dialog that is open while `input` is set. However the
 * dialog closes, it calls `onClose` and then refreshes the Cloudflare queries.
 */
export function CloudflareChange({ action, title, input, hostId, onClose }: CloudflareChangeProps) {
  const client = useQueryClient()
  function changeOpen(open: boolean) {
    if (open) return
    onClose()
    void client.invalidateQueries({ queryKey: ["cloudflare"] })
  }
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={changeOpen}
      title={title}
      target={{ name: action, version: CLOUDFLARE_ACTION_VERSION, ...(hostId !== undefined && { hostId, module: CLOUDFLARE_MODULE }) }}
      input={input ?? undefined}
    />
  )
}
