import type { ComponentType, LazyExoticComponent } from "react"
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
  containerActions?: ContainerAction[]
  hostCreate?: HostCreateExtension
  global?: GlobalPage
}
