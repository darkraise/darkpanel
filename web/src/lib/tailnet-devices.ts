import { readAction } from "@/api/client"
import type { TailnetDevice } from "@/api/types"

export const TAILNET_DEVICES_QUERY_KEY = ["tailnet-devices"] as const

export function listTailnetDevices(): Promise<{ devices: TailnetDevice[] }> {
  return readAction<{ devices: TailnetDevice[] }>("core.tailnetdevice.list", 1)
}

export function firstLabel(nodeName: string): string {
  return nodeName.split(".")[0] ?? nodeName
}

// deviceName names a device in a sentence: its node name's first label, or
// its stable id when the node has no name.
export function deviceName(d: TailnetDevice): string {
  return d.nodeName === "" ? d.stableId : firstLabel(d.nodeName)
}
