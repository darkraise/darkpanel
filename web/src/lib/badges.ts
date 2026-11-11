import type { BadgeVariant } from "darkraise-ui/components/badge"

export function stateVariant(state: string): BadgeVariant {
  switch (state) {
    case "committed":
    case "succeeded":
      return "default"
    case "updating":
    case "running":
    case "draining":
    case "waiting":
      return "secondary"
    case "failed":
    case "quarantined":
      return "destructive"
    default:
      return "outline"
  }
}
