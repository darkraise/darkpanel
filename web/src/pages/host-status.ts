import type { Host } from "@/api/types"
import type { Status } from "@/components/visual/status"

export function hostStatus(host: Host): { status: Status; label: string } {
  if (host.state === "pending") return { status: "info", label: "Pending" }
  if (host.state === "revoked") return { status: "error", label: "Revoked" }
  return host.online ? { status: "ok", label: "Online" } : { status: "error", label: "Offline" }
}
