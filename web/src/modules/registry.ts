import { lazy } from "react"
import {
  BrickWall,
  CalendarClock,
  Cloud,
  Cog,
  Container,
  ExternalLink,
  Globe,
  Info,
  Network,
  RadioTower,
  Server,
  ShieldBan,
  SlidersHorizontal,
  SquareTerminal,
  UserRound,
  Waypoints,
} from "lucide-react"
import type { Host, Session } from "@/api/types"
import { canOnHost } from "@/session/can"
import type { GlobalPage, HostCreateExtension, ModuleEntry } from "./types"

// One line per module (R14). Adding a module adds its line here and its
// folder modules/<id>/; no other module's files change.
export const moduleRegistry: ModuleEntry[] = [
  { id: "hostinfo", label: "Host information", icon: Info, routes: lazy(() => import("./hostinfo/routes")) },
  {
    id: "system",
    label: "System",
    icon: Server,
    routes: lazy(() => import("./system/routes")),
    unreported: lazy(() => import("./system/overview-tab").then((m) => ({ default: m.OverviewTab }))),
  },
  {
    id: "firewall",
    label: "Firewall",
    icon: BrickWall,
    routes: lazy(() => import("./firewall/routes")),
    containerActions: [
      { id: "firewall.allow", label: "Allow", render: lazy(() => import("./firewall/container-actions").then((m) => ({ default: m.AllowAction }))) },
      { id: "firewall.deny", label: "Deny", render: lazy(() => import("./firewall/container-actions").then((m) => ({ default: m.DenyAction }))) },
    ],
  },
  {
    id: "docker",
    label: "Docker",
    icon: Container,
    routes: lazy(() => import("./docker/routes")),
    unavailable: lazy(() => import("./system/docker-install-panel").then((m) => ({ default: m.DockerInstallPanel }))),
  },
  { id: "tailscale", label: "Tailscale", icon: Network, routes: lazy(() => import("./tailscale/routes")) },
  {
    id: "tailnet",
    label: "Tailnet",
    icon: Waypoints,
    hostCreate: { id: "tailnet.join", render: lazy(() => import("./tailnet/host-create")) },
    global: { path: "tailnet", label: "Tailnet", routes: lazy(() => import("./tailnet/global")) },
  },
  { id: "derp", label: "DERP", icon: RadioTower, global: { path: "derp", label: "DERP", routes: lazy(() => import("./derp/global")) } },
  {
    id: "npm",
    label: "Nginx Proxy Manager",
    icon: Globe,
    routes: lazy(() => import("./npm/routes")),
    containerActions: [
      { id: "npm.expose", label: "Expose", scope: "admin", render: lazy(() => import("./npm/container-actions").then((m) => ({ default: m.ExposeAction }))) },
    ],
  },
  {
    id: "cloudflare",
    label: "Cloudflare",
    icon: Cloud,
    containerActions: [
      {
        id: "cloudflare.expose",
        label: "Expose via tunnel",
        scope: "admin",
        render: lazy(() => import("./cloudflare/container-actions").then((m) => ({ default: m.ExposeAction }))),
      },
    ],
    global: { path: "cloudflare", label: "Cloudflare", routes: lazy(() => import("./cloudflare/global")) },
  },
  // Every panel action, the status read included, needs admin.
  { id: "panel", label: "Panel exposure", icon: ExternalLink, routes: lazy(() => import("./panel/routes")), scope: "admin" },
  { id: "services", label: "Services", icon: Cog, routes: lazy(() => import("./services/routes")) },
  { id: "users", label: "Users and SSH", icon: UserRound, routes: lazy(() => import("./users/routes")) },
  { id: "fail2ban", label: "Intrusion prevention", icon: ShieldBan, routes: lazy(() => import("./fail2ban/routes")) },
  // The console is a root shell and the host's files: admins only (ADR 0020).
  {
    id: "console",
    label: "Console",
    icon: SquareTerminal,
    routes: lazy(() => import("./console/routes")),
    scope: "admin",
    off: { allows: "Turning it on lets admins open a root terminal and manage files on this host." },
  },
  {
    id: "jobs",
    label: "Jobs",
    icon: CalendarClock,
    routes: lazy(() => import("./jobs/routes")),
    off: { allows: "Turning it on lets admins schedule commands to run on this host." },
  },
  { id: "tuning", label: "Tuning", icon: SlidersHorizontal, routes: lazy(() => import("./tuning/routes")) },
]

// HostModuleEntry is an entry that has host pages.
export type HostModuleEntry = ModuleEntry & { routes: NonNullable<ModuleEntry["routes"]> }

export type ModuleView =
  | { kind: "pages"; entry: HostModuleEntry }
  | { kind: "unavailable"; entry: HostModuleEntry; reason: string }
  | { kind: "unreported"; entry: HostModuleEntry & { unreported: NonNullable<ModuleEntry["unreported"]> } }
  | { kind: "off"; entry: HostModuleEntry & { off: NonNullable<ModuleEntry["off"]> } }
  | { kind: "not-enabled" }

function enabledOn(host: Host, moduleId: string): boolean {
  return host.modules.some((state) => state.id === moduleId && state.enabled)
}

// enabledModules lists, in registry order, the entries with host pages that
// the host has enabled. An entry with only a global page has no host tab.
export function enabledModules(entries: ModuleEntry[], host: Host): HostModuleEntry[] {
  return entries.filter((entry): entry is HostModuleEntry => entry.routes !== undefined && enabledOn(host, entry.id))
}

// hostTabs lists, in registry order, the module tabs of host a session may
// see: the enabled modules with host pages, and the modules turned off that
// say what turning them on allows (UX-12), each within the host's scope.
export function hostTabs(entries: ModuleEntry[], host: Host, session?: Session | null): HostModuleEntry[] {
  return entries.filter(
    (entry): entry is HostModuleEntry =>
      entry.routes !== undefined &&
      host.modules.some((state) => state.id === entry.id && (state.enabled || entry.off !== undefined)) &&
      canOnHost(host, entry.scope ?? "read", session),
  )
}

// moduleView decides what a module's tab shows: its pages, or the reason an
// enabled module is unavailable (R9), that it is turned off (UX-12), or
// that it is not enabled. A module the agent does not report at all shows
// its unreported view, if it has one.
export function moduleView(entries: ModuleEntry[], host: Host, moduleId: string): ModuleView {
  const entry = enabledModules(entries, host).find((e) => e.id === moduleId)
  const state = host.modules.find((s) => s.id === moduleId)
  const known = entries.find((e) => e.id === moduleId)
  if (state === undefined && known?.routes !== undefined && known.unreported !== undefined) {
    return { kind: "unreported", entry: { ...known, routes: known.routes, unreported: known.unreported } }
  }
  if (state !== undefined && !state.enabled && known?.routes !== undefined && known.off !== undefined) {
    return { kind: "off", entry: { ...known, routes: known.routes, off: known.off } }
  }
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
