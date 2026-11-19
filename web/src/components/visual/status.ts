import { CircleCheck, CircleMinus, CircleX, Info, TriangleAlert, type LucideIcon } from "lucide-react"

export type Status = "ok" | "warn" | "error" | "info" | "off"

// Each status has its own icon shape, so a status never rests on colour alone.
export const statusIcon: Record<Status, LucideIcon> = {
  ok: CircleCheck,
  warn: TriangleAlert,
  error: CircleX,
  info: Info,
  off: CircleMinus,
}

export const statusText: Record<Status, string> = {
  ok: "text-success-text",
  warn: "text-warning-text",
  error: "text-destructive-text",
  info: "text-info-text",
  off: "text-muted-foreground",
}
