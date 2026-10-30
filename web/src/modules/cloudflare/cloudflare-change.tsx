import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { CLOUDFLARE_ACTION_VERSION } from "./types"

export interface CloudflareChangeProps {
  action: string
  title: string
  input: Record<string, unknown> | null
  onClose: () => void
}

/**
 * Plans and applies one controller-side Cloudflare action in a dialog that is
 * open while `input` is set. However the dialog closes, it calls `onClose` and
 * then refreshes the Cloudflare queries.
 */
export function CloudflareChange({ action, title, input, onClose }: CloudflareChangeProps) {
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
      target={{ name: action, version: CLOUDFLARE_ACTION_VERSION }}
      input={input ?? undefined}
    />
  )
}
