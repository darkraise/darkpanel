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
    case "pending":
    case "downloading":
    case "staged":
    case "swapped":
    case "rolling-back":
      return "secondary"
    case "failed":
    case "quarantined":
    case "rolled-back":
      return "destructive"
    default:
      return "outline"
  }
}
