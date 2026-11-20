import { useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowRight, CircleCheck } from "lucide-react"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { readResource } from "@/api/client"
import type { Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import { StatusPill } from "@/components/visual/status-pill"
import { SystemChange, type PendingChange } from "./system-change"
import { TileError } from "./tile"
import { SYSTEM_MODULE, type Updates, type Upgradable } from "./types"

function Group({ label, tip, children }: { label: string; tip?: string; children: ReactNode }) {
  return (
    <section role="group" aria-label={label} className="flex flex-col gap-1">
      <h3 className="flex items-center gap-1 text-sm font-semibold">
        {label}
        {tip !== undefined && <InfoTip label={`About ${label.toLowerCase()}`}>{tip}</InfoTip>}
      </h3>
      {children}
    </section>
  )
}

function Versions({ from, to }: { from: string; to: string }) {
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-1 font-mono text-sm tabular-nums">
      <span className="break-all">{from}</span>
      <ArrowRight aria-label="to" className="size-3.5 shrink-0" />
      <span className="break-all">{to}</span>
    </span>
  )
}

function UpgradableRow({ pkg, checked, onToggle }: { pkg: Upgradable; checked: boolean; onToggle: (on: boolean) => void }) {
  const id = `upgrade-${pkg.name}`
  return (
    <li className="flex items-start gap-3 p-2">
      <Checkbox id={id} aria-label={pkg.name} checked={checked} onCheckedChange={(v) => onToggle(v === true)} className="mt-0.5" />
      <div className="grid min-w-0 flex-1 gap-1 @min-[40rem]:grid-cols-[minmax(0,2fr)_minmax(0,2fr)_auto] @min-[40rem]:items-center">
        <label htmlFor={id} className="font-mono text-sm break-all">
          {pkg.name}
        </label>
        <Versions from={pkg.from} to={pkg.to} />
        <span>{pkg.security && <StatusPill status="warn" label="Security" />}</span>
      </div>
    </li>
  )
}

function Rows({ children }: { children: ReactNode }) {
  return <ul className="divide-border flex flex-col divide-y rounded-md border">{children}</ul>
}

// UpdatesTab lists the upgradable packages, security first, with the
// packages a full upgrade removes and the held-back ones in their own
// groups (UI-5, UX-1). The selection upgrades through the change dialog.
export function UpdatesTab({ host }: { host: Host }) {
  // Until the operator changes it, the selection is the security updates.
  const [chosen, setChosen] = useState<ReadonlySet<string> | null>(null)
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
  const selected = chosen ?? new Set(security.map((u) => u.name))
  // A selection only ever names packages the latest read still lists.
  const names = data.upgradable.map((u) => u.name).filter((n) => selected.has(n))

  function toggle(name: string, on: boolean) {
    setChosen((cur) => {
      const next = new Set(cur ?? selected)
      if (on) next.add(name)
      else next.delete(name)
      return next
    })
  }

  const row = (pkg: Upgradable) => <UpgradableRow key={pkg.name} pkg={pkg} checked={selected.has(pkg.name)} onToggle={(on) => toggle(pkg.name, on)} />

  return (
    <div className="@container flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted-foreground text-sm tabular-nums">{`${names.length} of ${data.upgradable.length} selected`}</span>
        <Button
          size="sm"
          disabled={names.length === 0}
          onClick={() => setPending({ action: "system.packages.upgrade", title: `Upgrade ${names.length} ${names.length === 1 ? "package" : "packages"}`, input: { all: false, names } })}
        >
          Upgrade selected
        </Button>
      </div>
      {security.length > 0 && (
        <Group label="Security">
          <Rows>{security.map(row)}</Rows>
        </Group>
      )}
      {other.length > 0 && (
        <Group label="Other updates">
          <Rows>{other.map(row)}</Rows>
        </Group>
      )}
      {data.removals.length > 0 && (
        <Group label="Will be removed">
          <span className="flex flex-wrap gap-2">
            {data.removals.map((name) => (
              <Badge key={name} variant="destructive-subtle" className="font-mono break-all">
                {name}
              </Badge>
            ))}
          </span>
        </Group>
      )}
      {data.heldBack.length > 0 && (
        <Group label="Held back" tip="Held packages stay until unheld. Phased updates reach this host later.">
          <span className="flex flex-wrap gap-2">
            {data.heldBack.map((h) => (
              <StatusPill key={h.name} status="off" label={`${h.name} · ${h.reason}`} />
            ))}
          </span>
        </Group>
      )}
      <SystemChange hostId={host.id} pending={pending} onClose={() => setPending(null)} />
    </div>
  )
}
