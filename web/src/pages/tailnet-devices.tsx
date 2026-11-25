import { useLayoutEffect, useRef, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { PageHeader } from "darkraise-ui/layout"
import type { TailnetDevice } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { TailnetDevicesTable } from "@/components/tailnet-devices-table"
import { usePageTitle } from "@/lib/page-title"
import { TAILNET_DEVICES_QUERY_KEY, deviceName, listTailnetDevices } from "@/lib/tailnet-devices"
import { applied } from "@/lib/users"

export function TailnetDevicesPage() {
  usePageTitle("Tailscale devices")
  const client = useQueryClient()
  const list = useQuery({ queryKey: TAILNET_DEVICES_QUERY_KEY, queryFn: listTailnetDevices })
  const [forgetting, setForgetting] = useState<TailnetDevice | null>(null)
  const [forgotten, setForgotten] = useState<string | null>(null)
  const header = useRef<HTMLDivElement>(null)
  const heading = useRef<HTMLElement | null>(null)

  // PageHeader takes no ref, so its h1 is found once it renders; it takes
  // focus when the last device is forgotten.
  useLayoutEffect(() => {
    const h1 = header.current?.querySelector("h1") ?? null
    if (h1 !== null) h1.tabIndex = -1
    heading.current = h1
  }, [])

  return (
    <>
      <div ref={header}>
        <PageHeader title="Tailscale devices" description="Devices that may sign you in with Continue as without a code." />
      </div>
      <TailnetDevicesTable
        devices={list.data?.devices}
        isLoading={list.isPending}
        error={list.error}
        onForget={(d) => {
          setForgotten(null)
          setForgetting(d)
        }}
        forgotten={forgotten}
        emptyText="A device is remembered when you continue as yourself from it with your code."
        focusFallback={heading}
      />
      {forgetting !== null && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : setForgetting(null))}
          title={`Forget device ${deviceName(forgetting)}`}
          target={{ name: "core.tailnetdevice.forget", version: 1 }}
          input={{ stableId: forgetting.stableId }}
          onApplied={(change) => {
            if (!applied(change)) return
            setForgotten(forgetting.stableId)
            void client.invalidateQueries({ queryKey: TAILNET_DEVICES_QUERY_KEY })
            setForgetting(null)
          }}
        />
      )}
    </>
  )
}
