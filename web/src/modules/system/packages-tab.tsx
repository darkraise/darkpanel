import { useEffect, useId, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Lock, PackageCheck, Search } from "lucide-react"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "darkraise-ui/components/tooltip"
import { readAction, readResource } from "@/api/client"
import type { Host } from "@/api/types"
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
const STACK_CELL = "block py-0 whitespace-normal @min-[40rem]:table-cell @min-[40rem]:py-2"

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
  const search = useQuery({
    queryKey: ["resource", host.id, "system.search", term],
    queryFn: () => readAction<SearchHit[]>("system.packages.search", SYSTEM_ACTION_VERSION, { term }, host.id),
    enabled: valid && host.online,
  })
  return (
    <section className="flex flex-col gap-2" aria-labelledby={`${id}-title`}>
      <h3 id={`${id}-title`} className="text-sm font-medium">
        Install
      </h3>
      <div className="relative max-w-md">
        <Search aria-hidden className="text-muted-foreground absolute top-1/2 left-2 size-4 -translate-y-1/2" />
        <Input
          type="search"
          aria-label="Search packages"
          placeholder="Package name"
          autoComplete="off"
          className="ps-8"
          value={typed}
          disabled={!host.online}
          aria-invalid={typed.trim() !== "" && term === typed.trim() && !valid}
          aria-describedby={`${id}-hint`}
          onChange={(e) => setTyped(e.target.value)}
        />
      </div>
      <span id={`${id}-hint`} className="text-muted-foreground text-xs">
        Lowercase letters, digits, + . and -; at least 2 characters
      </span>
      {valid && search.isError && <TileError error={search.error} onRetry={() => void search.refetch()} />}
      {valid && search.isPending && host.online && <Skeleton className="h-9 w-full max-w-md" />}
      {valid && search.data?.length === 0 && <span className="text-sm">{`No package names contain ${term}`}</span>}
      {valid && search.data !== undefined && search.data.length > 0 && (
        <ul aria-label="Search results" className="divide-border flex max-w-2xl flex-col divide-y rounded-md border">
          {search.data.map((hit) => (
            <li key={hit.name} className="flex flex-wrap items-center gap-2 p-2">
              <span className="min-w-0 flex-1 font-mono text-sm break-all">{hit.name}</span>
              <span className="text-muted-foreground font-mono text-xs">{hit.candidate}</span>
              {hit.installed ? (
                <Badge variant="outline">
                  <PackageCheck aria-hidden className="size-3.5" />
                  Installed
                </Badge>
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

function ProtectedLock({ reason }: { reason: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className="inline-flex rounded-sm">
          <Lock role="img" aria-label={`Protected: ${reason}`} className="text-muted-foreground size-4" />
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{reason}</TooltipContent>
    </Tooltip>
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

  return (
    <section className="flex flex-col gap-2" aria-labelledby={`${id}-title`}>
      <h3 id={`${id}-title`} className="text-sm font-medium">
        Installed
      </h3>
      <div className="flex max-w-md flex-col gap-1">
        <Label htmlFor={`${id}-filter`} className="sr-only">
          Filter installed packages
        </Label>
        <Input
          id={`${id}-filter`}
          placeholder="Filter"
          autoComplete="off"
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
                <TableHead role="columnheader">Name</TableHead>
                <TableHead role="columnheader">Version</TableHead>
                <TableHead role="columnheader">Description</TableHead>
                <TableHead role="columnheader">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((p) => (
                <TableRow key={p.name} role="row" className={STACK_ROW}>
                  <TableCell role="cell" className={`${STACK_CELL} font-mono break-all`}>
                    {p.name}
                  </TableCell>
                  <TableCell role="cell" className={`${STACK_CELL} text-muted-foreground font-mono text-xs break-all`}>
                    {`${p.version} · ${p.arch}`}
                  </TableCell>
                  <TableCell role="cell" className={`${STACK_CELL} text-muted-foreground text-sm`}>
                    {p.description}
                  </TableCell>
                  <TableCell role="cell" className={`${STACK_CELL} @min-[40rem]:text-end`}>
                    {p.protected !== null ? (
                      <ProtectedLock reason={p.protected} />
                    ) : (
                      <Button
                        size="sm"
                        variant="outline"
                        aria-label={`Remove ${p.name}`}
                        disabled={!host.online}
                        onClick={() => onChange({ action: "system.packages.remove", title: `Remove ${p.name}`, input: { names: [p.name], purge: false } })}
                      >
                        Remove
                      </Button>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-muted-foreground text-sm tabular-nums">{`Showing ${rows.length} of ${matches.length}`}</span>
            {rows.length < matches.length && (
              <Button size="sm" variant="outline" onClick={() => setShown((n) => n + PAGE)}>
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
