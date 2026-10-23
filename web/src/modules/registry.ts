import { lazy } from "react"
import { Info } from "lucide-react"
import type { Host } from "@/api/types"
import type { GlobalPage, HostCreateExtension, ModuleEntry } from "./types"

// One line per module (R14). Adding a module adds its line here and its
// folder modules/<id>/; no other module's files change.
export const moduleRegistry: ModuleEntry[] = [
  { id: "hostinfo", label: "Host information", icon: Info, routes: lazy(() => import("./hostinfo/routes")) },
]

// HostModuleEntry is an entry that has host pages.
export type HostModuleEntry = ModuleEntry & { routes: NonNullable<ModuleEntry["routes"]> }

export type ModuleView =
  | { kind: "pages"; entry: HostModuleEntry }
  | { kind: "unavailable"; entry: HostModuleEntry; reason: string }
  | { kind: "not-enabled" }

function enabledOn(host: Host, moduleId: string): boolean {
  return host.modules.some((state) => state.id === moduleId && state.enabled)
}

// enabledModules lists, in registry order, the entries with host pages that
// the host has enabled. An entry with only a global page has no host tab.
export function enabledModules(entries: ModuleEntry[], host: Host): HostModuleEntry[] {
  return entries.filter((entry): entry is HostModuleEntry => entry.routes !== undefined && enabledOn(host, entry.id))
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

// globalPages lists, in registry order, the pages that belong to no host.
export function globalPages(entries: ModuleEntry[]): GlobalPage[] {
  return entries.flatMap((entry) => (entry.global === undefined ? [] : [entry.global]))
}

// hostCreateExtensions lists, in registry order, the "Add host" inputs of
// the modules enabled on host.
export function hostCreateExtensions(entries: ModuleEntry[], host: Host): HostCreateExtension[] {
  return entries.flatMap((entry) => (entry.hostCreate !== undefined && enabledOn(host, entry.id) ? [entry.hostCreate] : []))
}
