import {
  Box,
  BrickWall,
  Cloud,
  Container,
  Database,
  ExternalLink,
  Globe,
  Info,
  Layers,
  Network,
  Package,
  Puzzle,
  RadioTower,
  Server,
  Share2,
  Waypoints,
  type LucideIcon,
} from "lucide-react"
import { cn } from "darkraise-ui/lib"

// Module icons match the module registry's nav icons.
const icons: Record<string, { icon: LucideIcon; label: string }> = {
  hostinfo: { icon: Info, label: "Host information" },
  firewall: { icon: BrickWall, label: "Firewall" },
  docker: { icon: Container, label: "Docker" },
  tailscale: { icon: Network, label: "Tailscale" },
  tailnet: { icon: Waypoints, label: "Tailnet" },
  derp: { icon: RadioTower, label: "DERP" },
  npm: { icon: Globe, label: "Nginx Proxy Manager" },
  cloudflare: { icon: Cloud, label: "Cloudflare" },
  panel: { icon: ExternalLink, label: "Panel exposure" },
  system: { icon: Server, label: "System" },
  container: { icon: Box, label: "Container" },
  image: { icon: Layers, label: "Image" },
  volume: { icon: Database, label: "Volume" },
  network: { icon: Share2, label: "Network" },
  package: { icon: Package, label: "Package" },
}

export function ModuleIcon({ module, dimmed, className }: { module: string; dimmed?: boolean; className?: string }) {
  const { icon: Icon, label } = icons[module] ?? { icon: Puzzle, label: module }
  return (
    <span
      role="img"
      aria-label={dimmed ? `${label} (unavailable)` : label}
      data-module-icon={module}
      className={cn("inline-flex shrink-0", dimmed && "opacity-40", className)}
    >
      <Icon aria-hidden className="size-4" />
    </span>
  )
}
