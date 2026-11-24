import type { ComponentType, LazyExoticComponent } from "react"
import type { Host, Scope } from "@/api/types"
import type { LucideIcon } from "lucide-react"

// What a module's route bundle receives: the host whose pages it renders.
export interface ModuleRouteProps {
  hostId: string
}

// A container as the docker module's container page hands it to the
// container action slot.
export interface ContainerRef {
  hostId: string
  containerId: string
  name: string
  composeService?: string
  ports: { privatePort: number; protocol: "tcp" | "udp"; bindings: { hostIp: string; hostPort: number }[] }[]
}

// One button of the container action slot, rendered on the docker module's
// container page for every module enabled on the host.
export interface ContainerAction {
  id: string // "firewall.allow"
  label: string
  scope?: Scope // the host scope the action needs, operate when absent
  render: LazyExoticComponent<ComponentType<{ container: ContainerRef; onDone: () => void }>>
}

// Extra inputs of the "Add host" dialog.
export interface HostCreateExtension {
  id: string // "tailnet.join"
  // Renders extra inputs; value is merged into core.host.create's input.
  render: LazyExoticComponent<ComponentType<{ value: Record<string, unknown>; onChange: (v: Record<string, unknown>) => void }>>
}

// A page that belongs to no host, such as the tailnet.
export interface GlobalPage {
  path: string // "tailnet"; mounted at /<path>/*
  label: string
  routes: LazyExoticComponent<ComponentType>
}

// One line of the module route registry (spec section 17, R14). A module
// with only a global page has no routes, and so no host page.
export interface ModuleEntry {
  id: string
  label: string
  icon: LucideIcon
  routes?: LazyExoticComponent<ComponentType<ModuleRouteProps>> // host pages
  // Shown instead of the plain "unavailable" alert when the module is
  // enabled but unavailable on the host.
  unavailable?: LazyExoticComponent<ComponentType<{ host: Host; reason: string }>>
  // Shown when the host's agent reports no state for the module at all:
  // the agent predates it.
  unreported?: LazyExoticComponent<ComponentType<{ host: Host }>>
  containerActions?: ContainerAction[]
  hostCreate?: HostCreateExtension
  global?: GlobalPage
  // The scope on a host its host pages need, read when absent; a host whose
  // scope is lower has no tab for the module.
  scope?: Scope
}
