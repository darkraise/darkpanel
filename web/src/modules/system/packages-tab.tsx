import { useEffect, useId, useRef, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Lock, Search, Trash2 } from "lucide-react"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { readAction, readResource } from "@/api/client"
import type { Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import { StatusPill } from "@/components/visual/status-pill"
import { SystemChange, type PendingChange } from "./system-change"
import { TileError } from "./tile"
import { SYSTEM_ACTION_VERSION, SYSTEM_MODULE, type InstalledPackage } from "./types"

// The server's package-name pattern: a search term must match it too.
const TERM = /^[a-z0-9][a-z0-9+.-]{1,63}$/
const SEARCH_DELAY_MS = 300
// Rows shown per step, so 3000 installed packages stay responsive.
const PAGE = 100

interface SearchHit {
  name: string
  candidate: string
  installed: boolean
}

// Below 40rem of width each row stacks its cells; the explicit roles keep
// the table semantics a changed display would drop.
const STACK_ROW = "flex flex-col gap-1 py-2 @min-[40rem]:table-row @min-[40rem]:py-0"
const STACK_CELL = "block h-auto py-0 whitespace-normal @min-[40rem]:table-cell @min-[40rem]:h-[revert-layer] @min-[40rem]:py-2"

function useDebounced(value: string, delay: number): string {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delay)
    return () => clearTimeout(timer)
  }, [value, delay])
  return debounced
}

function SearchPanel({ host, onChange }: { host: Host; onChange: (p: PendingChange) => void }) {
  const id = useId()
  const [typed, setTyped] = useState("")
  const term = useDebounced(typed.trim(), SEARCH_DELAY_MS)
  const valid = TERM.test(term)
  const invalid = typed.trim() !== "" && term === typed.trim() && !valid
  const search = useQuery({
    queryKey: ["resource", host.id, "system.search", term],
    queryFn: () => readAction<SearchHit[]>("system.packages.search", SYSTEM_ACTION_VERSION, { term }, host.id),
    enabled: valid && host.online,
  })
  return (
    <section className="flex flex-col gap-2" aria-label="Install a package">
      <div className="relative">
        <Search aria-hidden className="text-muted-foreground absolute top-1/2 left-2 size-4 -translate-y-1/2" />
        <Input
          type="search"
          aria-label="Search packages"
          placeholder="Search packages"
          autoComplete="off"
          className="ps-8"
          value={typed}
          disabled={!host.online}
          aria-invalid={invalid}
          aria-describedby={`${id}-hint`}
          onChange={(e) => setTyped(e.target.value)}
        />
      </div>
      <span id={`${id}-hint`} className={invalid ? "text-muted-foreground text-xs" : "sr-only"}>
        Lowercase letters, digits, + . and -; at least 2 characters
      </span>
      {valid && search.isError && <TileError error={search.error} onRetry={() => void search.refetch()} />}
      {valid && search.isPending && host.online && <Skeleton className="h-9 w-full max-w-md" />}
      {valid && search.data?.length === 0 && <span className="text-sm">{`No package names contain ${term}`}</span>}
      {valid && search.data !== undefined && search.data.length > 0 && (
        <ul aria-label="Search results" className="divide-border flex flex-col divide-y rounded-md border">
          {search.data.map((hit) => (
            <li key={hit.name} className="flex flex-wrap items-center gap-2 p-2">
              <span className="min-w-0 flex-1 font-mono text-sm break-all">{hit.name}</span>
              <span className="font-mono text-sm tabular-nums break-all">{hit.candidate}</span>
              {hit.installed ? (
                <StatusPill status="ok" label="Installed" />
              ) : (
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={`Install ${hit.name}`}
                  onClick={() => onChange({ action: "system.packages.install", title: `Install ${hit.name}`, input: { names: [hit.name] } })}
                >
                  Install
                </Button>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function ProtectedLock({ name, reason }: { name: string; reason: string }) {
  return (
    <span className="inline-flex items-center gap-1 text-sm">
      <Lock role="img" aria-label="Protected" className="text-muted-foreground size-4" />
      <InfoTip label={`Why ${name} is protected`}>{reason}</InfoTip>
    </span>
  )
}

function InstalledPanel({ host, onChange }: { host: Host; onChange: (p: PendingChange) => void }) {
  const id = useId()
  const [filter, setFilter] = useState("")
  const [shown, setShown] = useState(PAGE)
  const packages = useQuery({
    queryKey: ["resource", host.id, "system.packages"],
    queryFn: () => readResource<InstalledPackage[]>(host.id, SYSTEM_MODULE, "packages"),
    enabled: host.online,
  })
  const needle = filter.trim().toLowerCase()
  const matches = (packages.data ?? []).filter((p) => needle === "" || p.name.includes(needle) || p.description.toLowerCase().includes(needle))
  const rows = matches.slice(0, shown)
  const countRef = useRef<HTMLSpanElement>(null)
  // A name installed for two architectures lists one row each; removal names
  // the package alone, which apt reads as the native one, so only the first
  // row of such a name offers Remove.
  const seen = new Map<string, number>()
  for (const p of packages.data ?? []) seen.set(p.name, (seen.get(p.name) ?? 0) + 1)
  const firstOfName = new Set<string>()

  return (
    <section className="flex flex-col gap-2" aria-labelledby={`${id}-title`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 id={`${id}-title`} className="text-sm font-semibold">
          Installed{" "}
          {packages.data !== undefined && <span className="text-muted-foreground font-mono tabular-nums">{packages.data.length}</span>}
        </h3>
        <Label htmlFor={`${id}-filter`} className="sr-only">
          Filter installed packages
        </Label>
        <Input
          id={`${id}-filter`}
          placeholder="Filter"
          autoComplete="off"
          className="max-w-56"
          value={filter}
          onChange={(e) => {
            setFilter(e.target.value)
            setShown(PAGE)
          }}
        />
      </div>
      {packages.isError && <TileError error={packages.error} onRetry={() => void packages.refetch()} />}
      {packages.isPending && host.online && (
        <div className="flex flex-col gap-2" aria-busy>
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      )}
      {packages.data !== undefined && (
        <>
          <Table role="table" aria-label="Installed packages">
            <TableHeader className="hidden @min-[40rem]:table-header-group">
              <TableRow role="row">
                <TableHead role="columnheader">Package</TableHead>
                <TableHead role="columnheader">Version</TableHead>
                <TableHead role="columnheader" className="text-end">
                  Action
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((p) => {
                const multiarch = (seen.get(p.name) ?? 0) > 1
                const removable = !firstOfName.has(p.name)
                firstOfName.add(p.name)
                return (
                <TableRow key={`${p.name}:${p.arch}`} role="row" className={STACK_ROW}>
                  <TableCell role="cell" className={`${STACK_CELL} font-mono text-sm break-all`}>
                    {p.name}
                  </TableCell>
                  <TableCell role="cell" className={`${STACK_CELL} font-mono text-sm tabular-nums break-all`}>
                    {multiarch ? `${p.version} · ${p.arch}` : p.version}
                  </TableCell>
                  <TableCell role="cell" className={`${STACK_CELL} @min-[40rem]:text-end`}>
                    {p.protected !== null ? (
                      <ProtectedLock name={p.name} reason={p.protected} />
                    ) : !removable ? (
                      <InfoTip label={`Why ${p.name} (${p.arch}) has no Remove`}>
                        Only one architecture of a package can be removed here: Remove on the first row removes the host&apos;s own. Remove {p.name}:{p.arch} with apt on
                        the host.
                      </InfoTip>
                    ) : (
                      <Button
                        size="sm"
                        variant="outline"
                        aria-label={`Remove ${p.name}`}
                        disabled={!host.online}
                        onClick={() => onChange({ action: "system.packages.remove", title: `Remove ${p.name}`, input: { names: [p.name], purge: false } })}
                      >
                        <Trash2 aria-hidden />
                        Remove
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
                )
              })}
            </TableBody>
          </Table>
          {matches.length === 0 && <p className="text-muted-foreground text-sm">{`No installed package matches ${filter.trim()}`}</p>}
          <div className="flex flex-wrap items-center gap-2">
            <span ref={countRef} tabIndex={-1} className="text-muted-foreground text-sm tabular-nums outline-none">{`Showing ${rows.length} of ${matches.length}`}</span>
            {rows.length < matches.length && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => {
                  // The button goes with the last page: keep focus on the count.
                  if (rows.length + PAGE >= matches.length) setTimeout(() => countRef.current?.focus())
                  setShown((n) => n + PAGE)
                }}
              >
                Show more
              </Button>
            )}
          </div>
        </>
      )}
    </section>
  )
}

// PackagesTab searches the configured sources to install a package and
// lists the installed ones to remove one (UI-5, UX-2, UX-3). A protected
// package has a lock with its reason instead of a remove button.
export function PackagesTab({ host }: { host: Host }) {
  const [pending, setPending] = useState<PendingChange | null>(null)
  if (!host.online) return <EmptyState title="Host offline" description="Packages read again once the host is back." />
  return (
    <div className="@container flex flex-col gap-6">
      <SearchPanel host={host} onChange={setPending} />
      <InstalledPanel host={host} onChange={setPending} />
      <SystemChange hostId={host.id} pending={pending} onClose={() => setPending(null)} />
    </div>
  )
}
