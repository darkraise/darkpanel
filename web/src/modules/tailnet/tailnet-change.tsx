import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { TAILNET_ACTION_VERSION } from "./types"

export function TailnetChange({ action, title, input, onClose }: {
  action: string
  title: string
  input: Record<string, unknown> | null
  onClose: () => void
}) {
  const client = useQueryClient()
  function changeOpen(open: boolean) {
    if (open) return
    onClose()
    void client.invalidateQueries({ queryKey: ["tailnet"] })
  }
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={changeOpen}
      title={title}
      target={{ name: action, version: TAILNET_ACTION_VERSION }}
      input={input ?? undefined}
    />
  )
}
