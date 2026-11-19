import { useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowRight, CircleCheck, Pause, ShieldAlert, Trash2, type LucideIcon } from "lucide-react"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { readResource } from "@/api/client"
import type { Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import { SystemChange, type PendingChange } from "./system-change"
import { TileError } from "./tile"
import { SYSTEM_MODULE, type Updates, type Upgradable } from "./types"

function Group({ icon: Icon, label, count, tip, children }: { icon: LucideIcon; label: string; count: number; tip?: string; children: ReactNode }) {
  return (
    <section role="group" aria-label={label} className="flex flex-col gap-2">
      <h3 className="flex items-center gap-2 text-sm font-medium">
        <Icon aria-hidden className="text-muted-foreground size-4" />
        {label}
        <span className="text-muted-foreground font-mono tabular-nums">{count}</span>
        {tip !== undefined && <InfoTip label={`About ${label.toLowerCase()}`}>{tip}</InfoTip>}
      </h3>
      <ul className="divide-border flex flex-col divide-y rounded-md border">{children}</ul>
    </section>
  )
}

function Versions({ from, to }: { from: string; to: string }) {
  return (
    <span className="text-muted-foreground flex min-w-0 flex-wrap items-center gap-1 font-mono text-xs tabular-nums">
      <span className="break-all">{from}</span>
      <ArrowRight aria-label="to" className="size-3.5 shrink-0" />
      <span className="text-foreground break-all">{to}</span>
    </span>
  )
}

function UpgradableRow({ pkg, checked, onToggle }: { pkg: Upgradable; checked: boolean; onToggle: (on: boolean) => void }) {
  const id = `upgrade-${pkg.name}`
  return (
    <li className="flex items-start gap-3 p-2">
      <Checkbox id={id} aria-label={pkg.name} checked={checked} onCheckedChange={(v) => onToggle(v === true)} className="mt-0.5" />
      <div className="grid min-w-0 flex-1 gap-1 @min-[40rem]:grid-cols-[minmax(0,2fr)_minmax(0,2fr)_minmax(0,1fr)] @min-[40rem]:items-center">
        <label htmlFor={id} className="font-mono text-sm break-all">
          {pkg.name}
        </label>
        <Versions from={pkg.from} to={pkg.to} />
        <span className="text-muted-foreground truncate text-xs">{pkg.origin}</span>
      </div>
    </li>
  )
}

// UpdatesTab lists the upgradable packages, security first, with the
// packages a full upgrade removes and the held-back ones in their own
// groups (UI-5, UX-1). The selection upgrades through the change dialog.
export function UpdatesTab({ host }: { host: Host }) {
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())
  const [pending, setPending] = useState<PendingChange | null>(null)
  const updates = useQuery({
    queryKey: ["resource", host.id, "system.updates"],
    queryFn: () => readResource<Updates>(host.id, SYSTEM_MODULE, "updates"),
    enabled: host.online,
  })
  if (!host.online) return <EmptyState title="Host offline" description="Updates read again once the host is back." />
  if (updates.isError) return <TileError error={updates.error} onRetry={() => void updates.refetch()} />
  if (updates.data === undefined) {
    return (
      <div className="flex flex-col gap-2" aria-busy>
        {Array.from({ length: 6 }, (_, i) => (
          <Skeleton key={i} className="h-9 w-full" />
        ))}
      </div>
    )
  }
  const data = updates.data
  if (data.upgradable.length === 0 && data.removals.length === 0 && data.heldBack.length === 0) {
    return <EmptyState icon={<CircleCheck aria-hidden />} title="Up to date" />
  }
  const security = data.upgradable.filter((u) => u.security)
  const other = data.upgradable.filter((u) => !u.security)
  // A selection only ever names packages the latest read still lists.
  const names = data.upgradable.map((u) => u.name).filter((n) => selected.has(n))

  function toggle(name: string, on: boolean) {
    setSelected((cur) => {
      const next = new Set(cur)
      if (on) next.add(name)
      else next.delete(name)
      return next
    })
  }

  const row = (pkg: Upgradable) => <UpgradableRow key={pkg.name} pkg={pkg} checked={selected.has(pkg.name)} onToggle={(on) => toggle(pkg.name, on)} />

  return (
    <div className="@container flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          disabled={names.length === 0}
          onClick={() => setPending({ action: "system.packages.upgrade", title: `Upgrade ${names.length} ${names.length === 1 ? "package" : "packages"}`, input: { all: false, names } })}
        >
          Upgrade selected
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={data.upgradable.length === 0}
          onClick={() => setPending({ action: "system.packages.upgrade", title: `Upgrade ${host.name}`, input: { all: true, names: [] } })}
        >
          Upgrade all
        </Button>
        <span className="text-muted-foreground text-sm tabular-nums">{`${names.length} selected`}</span>
      </div>
      {security.length > 0 && (
        <Group icon={ShieldAlert} label="Security" count={security.length}>
          {security.map(row)}
        </Group>
      )}
      {other.length > 0 && (
        <Group icon={ArrowRight} label="Other updates" count={other.length}>
          {other.map(row)}
        </Group>
      )}
      {data.removals.length > 0 && (
        <Group icon={Trash2} label="Will be removed" count={data.removals.length} tip="A full upgrade removes these packages. Upgrading selected packages never does.">
          {data.removals.map((name) => (
            <li key={name} className="p-2 font-mono text-sm break-all">
              {name}
            </li>
          ))}
        </Group>
      )}
      {data.heldBack.length > 0 && (
        <Group
          icon={Pause}
          label="Held back"
          count={data.heldBack.length}
          tip="Held packages are pinned with apt-mark hold. Phased ones reach this host later in a staged rollout."
        >
          {data.heldBack.map((h) => (
            <li key={h.name} className="flex flex-wrap items-center gap-2 p-2">
              <span className="min-w-0 font-mono text-sm break-all">{h.name}</span>
              <span className="text-muted-foreground font-mono text-xs">{h.version}</span>
              <Badge variant="outline">{h.reason}</Badge>
            </li>
          ))}
        </Group>
      )}
      <SystemChange hostId={host.id} pending={pending} onClose={() => setPending(null)} />
    </div>
  )
}
