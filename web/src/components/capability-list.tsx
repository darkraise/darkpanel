import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"

export interface Capability {
  name: string
  state: "ok" | "missing" | "unverified"
  detail?: string
}

// A probe that never reached the provider proved nothing about the permission.
const UNREACHED = ["cloud.unavailable", "cloud.rate_limited"]

function badge(c: Capability): { label: string; variant: BadgeVariant } {
  if (c.state === "ok") return { label: "ok", variant: "default" }
  if (c.state === "unverified") return { label: "unverified", variant: "secondary" }
  if (c.detail !== undefined && UNREACHED.includes(c.detail)) return { label: "could not check", variant: "outline" }
  return { label: "missing", variant: "destructive" }
}

export function CapabilityList({ capabilities }: { capabilities: Capability[] }) {
  if (capabilities.length === 0) return <p className="text-sm text-muted-foreground">No capabilities reported.</p>
  return (
    <ul aria-label="Capabilities" className="flex flex-col gap-2">
      {capabilities.map((c) => {
        const { label, variant } = badge(c)
        return (
          <li key={c.name} className="flex flex-wrap items-center gap-2">
            <span className="font-mono text-sm">{c.name}</span>
            <Badge variant={variant} size="sm">{label}</Badge>
            {c.detail !== undefined && c.detail !== "" && <span className="text-sm text-muted-foreground">{c.detail}</span>}
          </li>
        )
      })}
    </ul>
  )
}
