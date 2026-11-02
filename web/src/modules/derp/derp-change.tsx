import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { DERP_ACTION_VERSION, DERP_MODULE } from "./types"

export function DerpChange({ action, title, input, onClose }: {
  action: string
  title: string
  input: Record<string, unknown> | null
  onClose: () => void
}) {
  const client = useQueryClient()
  function changeOpen(open: boolean) {
    if (open) return
    onClose()
    void client.invalidateQueries({ queryKey: [DERP_MODULE] })
  }
  return (
    <ChangeDialog
      open={input !== null}
      onOpenChange={changeOpen}
      title={title}
      target={{ name: action, version: DERP_ACTION_VERSION }}
      input={input ?? undefined}
    />
  )
}
