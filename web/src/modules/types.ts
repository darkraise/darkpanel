import type { ComponentType, LazyExoticComponent } from "react"
import type { LucideIcon } from "lucide-react"

// What a module's route bundle receives: the host whose pages it renders.
export interface ModuleRouteProps {
  hostId: string
}

// One line of the module route registry (spec section 17, R14).
export interface ModuleEntry {
  id: string
  label: string
  icon: LucideIcon
  routes: LazyExoticComponent<ComponentType<ModuleRouteProps>>
}
