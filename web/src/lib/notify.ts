import type { ChannelKind, HostGroup, NotifyChannel, Severity } from "@/api/types"

export const CHANNELS_QUERY_KEY = ["notify-channels"] as const

export const KIND_LABELS: Record<ChannelKind, string> = { ntfy: "ntfy", telegram: "Telegram", discord: "Discord", email: "Email", webhook: "Webhook" }
export const CHANNEL_KINDS: ChannelKind[] = ["ntfy", "telegram", "discord", "email", "webhook"]

// The event kinds a channel may pick, in the groups the form offers; none
// picked receives every kind.
export const EVENT_GROUPS: { id: string; label: string; kinds: string[] }[] = [
  { id: "alerts", label: "Alerts", kinds: ["alert.firing", "alert.resolved"] },
  { id: "changes", label: "Failed changes", kinds: ["change.failed", "change.auto_reverted"] },
  { id: "updates", label: "Failed rollouts and updates", kinds: ["rollout.failed", "update.rolled_back"] },
  { id: "delivery", label: "Failed deliveries", kinds: ["notify.delivery_failed"] },
]

const SEVERITY_TEXT: Record<Severity, string> = { info: "Info and above", warning: "Warning and above", critical: "Critical" }

export function routingText(c: Pick<NotifyChannel, "minSeverity" | "kinds" | "groupIds">, groups: HostGroup[] | undefined): string {
  const chosen = EVENT_GROUPS.filter((g) => g.kinds.some((k) => c.kinds.includes(k))).map((g) => g.label.toLowerCase())
  const events = c.kinds.length === 0 ? "all events" : chosen.length === 0 ? "chosen events" : chosen.length === 1 ? `${chosen[0]} only` : chosen.join(", ")
  const names = c.groupIds.map((id) => groups?.find((g) => g.id === id)?.name ?? id)
  const hosts = names.length === 0 ? "all hosts" : `${names.length === 1 ? "group" : "groups"} ${names.join(", ")}`
  return `${SEVERITY_TEXT[c.minSeverity]}, ${events}, ${hosts}`
}

export function lastDeliveryText(c: NotifyChannel, format: (at: string) => string): string {
  const d = c.lastDelivery
  if (d === null) return "No deliveries yet"
  return d.state === "sent" ? `Delivered ${format(d.at)}` : `Failed: ${d.error ?? "unknown"}`
}
