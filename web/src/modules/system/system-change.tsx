import { useLayoutEffect, useRef, type ReactNode } from "react"
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
  const opener = useRef<{ trigger: HTMLElement; area: HTMLElement | null } | null>(null)
  // The trigger may be gone or disabled once the change applies (a removed
  // package's row, the Reboot button of a scheduled reboot): focus then goes
  // to the tile or section the trigger sat in, not to the page.
  useLayoutEffect(() => {
    if (pending === null) return
    const trigger = document.activeElement
    if (trigger instanceof HTMLElement && trigger !== document.body) {
      opener.current = { trigger, area: trigger.closest<HTMLElement>("section, [role='region'], [role='tabpanel']") }
    }
  }, [pending])
  const refresh = () => {
    void client.invalidateQueries({ queryKey: ["resource", hostId] })
    void client.invalidateQueries({ queryKey: systemChangesKey(hostId) })
  }
  const keepFocus = () => {
    const o = opener.current
    opener.current = null
    if (o === null) return
    setTimeout(() => {
      const lost = !o.trigger.isConnected || o.trigger.matches(":disabled")
      if (!lost || o.area === null || !o.area.isConnected) return
      if (!o.area.hasAttribute("tabindex")) o.area.setAttribute("tabindex", "-1")
      o.area.focus()
    }, 50)
  }
  if (pending === null) return null
  return (
    <ChangeDialog
      open
      onOpenChange={(open) => {
        if (open) return
        onClose()
        refresh()
        keepFocus()
      }}
      title={pending.title}
      note={pending.note}
      target={{ name: pending.action, version: SYSTEM_ACTION_VERSION, hostId, module: SYSTEM_MODULE }}
      input={pending.input}
      onApplied={refresh}
    />
  )
}
