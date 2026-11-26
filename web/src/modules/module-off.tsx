import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { HOSTS_QUERY_KEY } from "@/api/client"
import type { Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { applied } from "@/lib/users"
import { useCanOnHost } from "@/session/can"
import type { ModuleEntry } from "./types"

// ModuleOff is the tab of a module turned off on the host (UX-12): what
// turning it on allows and, for admins, Enable through core.module.enable.
export function ModuleOff({ host, entry }: { host: Host; entry: ModuleEntry & { off: { allows: string } } }) {
  const admin = useCanOnHost("admin")
  const client = useQueryClient()
  const [enabling, setEnabling] = useState(false)
  const Icon = entry.icon
  return (
    <>
      <h2 className="sr-only">{entry.label}</h2>
      <EmptyState
        data-testid="state-off"
        icon={<Icon aria-hidden="true" />}
        title="Off on this host"
        description={entry.off.allows}
        action={admin ? <Button onClick={() => setEnabling(true)}>Enable</Button> : undefined}
      />
      {enabling && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : setEnabling(false))}
          title={`Enable ${entry.label.toLowerCase()} on ${host.name}`}
          target={{ name: "core.module.enable", version: 1 }}
          input={{ hostId: host.id, moduleId: entry.id }}
          onApplied={(change) => {
            if (applied(change)) void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
          }}
        />
      )}
    </>
  )
}
