import { useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowRight, CircleCheck } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Table, TableBody, TableCell, TableRow } from "darkraise-ui/components/table"
import { readResource } from "@/api/client"
import type { Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import { StatusPill } from "@/components/visual/status-pill"
import { SystemChange, type PendingChange } from "./system-change"
import { TileError } from "./tile"
import { SYSTEM_MODULE, type Updates, type Upgradable } from "./types"
import { Can } from "@/session/can"

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
      {from !== "" && (
        <>
          <span className="break-all">{from}</span>
          <ArrowRight aria-label="to" className="size-3.5 shrink-0" />
        </>
      )}
      <span className="break-all">{to}</span>
    </span>
  )
}

// A row with no installed version is a package a full upgrade installs (a
// new kernel, for one): an upgrade of selected packages cannot name it, so
// it comes only with Upgrade all.
const selectable = (u: Upgradable) => u.from !== ""

// Below 40rem of width each row stacks its cells; the explicit roles keep
// the table semantics a changed display would drop.
const STACK_ROW = "flex flex-col gap-1 py-2 @min-[40rem]:table-row @min-[40rem]:py-0"
const STACK_CELL = "block h-auto py-0 whitespace-normal @min-[40rem]:table-cell @min-[40rem]:h-[revert-layer] @min-[40rem]:py-2"

function UpgradableRow({ pkg, checked, onToggle }: { pkg: Upgradable; checked: boolean; onToggle: (on: boolean) => void }) {
  const id = `upgrade-${pkg.name}`
  const fresh = !selectable(pkg)
  return (
    <TableRow role="row" className={STACK_ROW}>
      <TableCell role="cell" className={`${STACK_CELL} @min-[40rem]:max-w-96`}>
        <span className="flex items-start gap-3">
          <Checkbox id={id} aria-label={pkg.name} checked={checked && !fresh} disabled={fresh} onCheckedChange={(v) => onToggle(v === true)} className="mt-0.5 shrink-0" />
          <label htmlFor={id} className="min-w-0 font-mono text-sm break-all">
            {pkg.name}
          </label>
        </span>
      </TableCell>
      <TableCell role="cell" className={`${STACK_CELL} ps-9 @min-[40rem]:ps-2`}>
        <Versions from={pkg.from} to={pkg.to} />
      </TableCell>
      <TableCell role="cell" className={`${STACK_CELL} ps-9 @min-[40rem]:ps-2`}>
        <span className="flex flex-wrap items-center gap-1">
          {pkg.security && <StatusPill status="warn" label="Security" />}
          {fresh && (
            <span className="inline-flex items-center">
              <StatusPill status="info" label="New" />
              <InfoTip label={`Why ${pkg.name} cannot be selected`}>A full upgrade installs it; Upgrade all on the Overview tab brings it.</InfoTip>
            </span>
          )}
        </span>
      </TableCell>
    </TableRow>
  )
}

function Rows({ label, children }: { label: string; children: ReactNode }) {
  return (
    <Table role="table" aria-label={label}>
      <TableBody>{children}</TableBody>
    </Table>
  )
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
  const notices = (
    <>
      {data.failedSources.length > 0 && (
        <Alert variant="warning">
          <AlertTitle as="h3">{`${data.failedSources.length} ${data.failedSources.length === 1 ? "source" : "sources"} failed at the last refresh`}</AlertTitle>
          <AlertDescription className="font-mono text-xs break-all">{data.failedSources.join(", ")}</AlertDescription>
        </Alert>
      )}
      <SystemChange hostId={host.id} pending={pending} onClose={() => setPending(null)} />
    </>
  )
  // An interrupted dpkg leaves the lists unread: that is not "up to date".
  if (data.dpkgInterrupted) {
    return (
      <div className="flex flex-col gap-4">
        <Alert variant="destructive" role="alert">
          <AlertTitle as="h3">Package installation was interrupted</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <span>The updates cannot be read until the packages are repaired.</span>
            <Can need="operate"><Button size="sm" variant="outline" onClick={() => setPending({ action: "system.packages.repair", title: "Repair packages", input: {} })}>
              Repair packages
            </Button></Can>
          </AlertDescription>
        </Alert>
        {notices}
      </div>
    )
  }
  if (data.upgradable.length === 0 && data.removals.length === 0 && data.heldBack.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        <EmptyState icon={<CircleCheck aria-hidden />} title="Up to date" />
        {notices}
      </div>
    )
  }
  const security = data.upgradable.filter((u) => u.security)
  const other = data.upgradable.filter((u) => !u.security)
  const choosable = data.upgradable.filter(selectable)
  const selected = chosen ?? new Set(security.filter(selectable).map((u) => u.name))
  // A selection only ever names packages the latest read still lists.
  const names = choosable.map((u) => u.name).filter((n) => selected.has(n))

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
        <span className="text-muted-foreground text-sm tabular-nums">{`${names.length} of ${choosable.length} selected`}</span>
        <Can need="operate"><Button
          size="sm"
          disabled={names.length === 0}
          onClick={() => setPending({ action: "system.packages.upgrade", title: `Upgrade ${names.length} ${names.length === 1 ? "package" : "packages"}`, input: { all: false, names } })}
        >
          Upgrade selected
        </Button></Can>
      </div>
      {security.length > 0 && (
        <Group label="Security">
          <Rows label="Security updates">{security.map(row)}</Rows>
        </Group>
      )}
      {other.length > 0 && (
        <Group label="Other updates">
          <Rows label="Other updates">{other.map(row)}</Rows>
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
      {notices}
    </div>
  )
}
