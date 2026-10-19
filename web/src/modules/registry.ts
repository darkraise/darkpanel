import { lazy } from "react"
import { Info } from "lucide-react"
import type { Host } from "@/api/types"
import type { ModuleEntry } from "./types"

// One line per module (R14). Adding a module adds its line here and its
// folder modules/<id>/; no other module's files change.
export const moduleRegistry: ModuleEntry[] = [
  { id: "hostinfo", label: "Host information", icon: Info, routes: lazy(() => import("./hostinfo/routes")) },
]

export type ModuleView =
  | { kind: "pages"; entry: ModuleEntry }
  | { kind: "unavailable"; entry: ModuleEntry; reason: string }
  | { kind: "not-enabled" }

// enabledModules lists, in registry order, the entries the host has enabled.
export function enabledModules(entries: ModuleEntry[], host: Host): ModuleEntry[] {
  return entries.filter((entry) => host.modules.some((state) => state.id === entry.id && state.enabled))
}

// moduleView decides what a module's tab shows: its pages, or the reason an
// enabled module is unavailable (R9), or that it is not enabled.
export function moduleView(entries: ModuleEntry[], host: Host, moduleId: string): ModuleView {
  const entry = enabledModules(entries, host).find((e) => e.id === moduleId)
  const state = host.modules.find((s) => s.id === moduleId)
  if (entry === undefined || state === undefined) return { kind: "not-enabled" }
  if (!state.available) return { kind: "unavailable", entry, reason: state.reason ?? "The module is unavailable on this host." }
  return { kind: "pages", entry }
}
