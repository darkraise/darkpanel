import type { ReactNode } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { ChangeDialog } from "@/components/change-dialog"
import { SYSTEM_ACTION_VERSION, SYSTEM_MODULE, systemChangesKey } from "./types"

export interface PendingChange {
  action: string // a mutating system action, such as "system.packages.upgrade"
  title: string
  input: Record<string, unknown>
  note?: ReactNode
}

// SystemChange plans and applies one system action through the change
// dialog, then reads the host's resources and changes again.
export function SystemChange({ hostId, pending, onClose }: { hostId: string; pending: PendingChange | null; onClose: () => void }) {
  const client = useQueryClient()
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ["resource", hostId] })
    void client.invalidateQueries({ queryKey: systemChangesKey(hostId) })
  }
  if (pending === null) return null
  return (
    <ChangeDialog
      open
      onOpenChange={(open) => {
        if (open) return
        onClose()
        refresh()
      }}
      title={pending.title}
      note={pending.note}
      target={{ name: pending.action, version: SYSTEM_ACTION_VERSION, hostId, module: SYSTEM_MODULE }}
      input={pending.input}
      onApplied={refresh}
    />
  )
}
