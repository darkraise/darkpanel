import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowDown } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Switch } from "darkraise-ui/components/switch"
import { errorMessage, readAction } from "@/api/client"
import { StatusPill } from "@/components/visual/status-pill"
import { streamUrl } from "@/modules/docker/stream"
import { MAX_LINES, reconnectDelay, type JournalEntry } from "./journal"

const PRIORITIES = ["emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"]
const SINCE: [string, string, number][] = [
  ["15m", "Last 15 minutes", 15 * 60],
  ["1h", "Last hour", 3600],
  ["1d", "Last day", 86400],
  ["all", "Any time", 0],
]


type FollowStatus = "connecting" | "following" | "reconnecting" | "ended"

interface Filters {
  unit: string
  priority: string // "" for every priority
  since: string
}

function params(f: Filters, now: number): Record<string, string> {
  const p: Record<string, string> = {}
  if (f.unit !== "") p.unit = f.unit
  if (f.priority !== "") p.priority = f.priority
  const window = SINCE.find(([id]) => id === f.since)?.[2] ?? 0
  if (window > 0) p.since = String(Math.floor(now / 1000) - window)
  return p
}

// useFollow follows the journal with f, reconnecting a dropped stream with a
// growing delay from just after the last entry it holds.
function useFollow(hostId: string, f: Filters, on: boolean): { entries: JournalEntry[]; status: FollowStatus } {
  const [entries, setEntries] = useState<JournalEntry[]>([])
  const [status, setStatus] = useState<FollowStatus>("connecting")
  const key = `${hostId} ${f.unit} ${f.priority} ${f.since} ${String(on)}`
  const [seen, setSeen] = useState(key)
  if (seen !== key) {
    setSeen(key)
    setEntries([])
    setStatus("connecting")
  }

  useEffect(() => {
    if (!on) return
    let live = true
    let source: EventSource | null = null
    let timer: ReturnType<typeof setTimeout> | undefined
    let failures = 0
    let last = 0
    const open = () => {
      const p = params(f, Date.now())
      if (last > 0) p.since = String(Math.floor(last / 1_000_000))
      source = new EventSource(streamUrl(hostId, "services", "journal-follow", p))
      source.addEventListener("data", (event) => {
        if (!live) return
        let entry: JournalEntry
        try {
          entry = JSON.parse((event as MessageEvent<string>).data) as JournalEntry
        } catch {
          return
        }
        if (entry.time <= last) return
        last = entry.time
        failures = 0
        setStatus("following")
        setEntries((prev) => [...prev, entry].slice(-MAX_LINES))
      })
      source.addEventListener("open", () => {
        if (live) setStatus("following")
      })
      source.addEventListener("end", () => {
        if (!live) return
        source?.close()
        setStatus("ended")
      })
      source.addEventListener("error", () => {
        if (!live) return
        source?.close()
        setStatus("reconnecting")
        timer = setTimeout(open, reconnectDelay(failures++))
      })
    }
    open()
    return () => {
      live = false
      clearTimeout(timer)
      source?.close()
    }
    // f's fields are in key.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key])

  return seen === key ? { entries, status } : { entries: [], status: "connecting" }
}

function time(us: number): string {
  return new Date(us / 1000).toLocaleTimeString([], { hour12: false })
}

function Lines({ entries, follow, onPaused }: { entries: JournalEntry[]; follow: boolean; onPaused: (paused: boolean) => void }) {
  const box = useRef<HTMLDivElement>(null)
  const pinned = useRef(true)
  useLayoutEffect(() => {
    const el = box.current
    if (el !== null && follow && pinned.current) el.scrollTop = el.scrollHeight
  }, [entries, follow])
  return (
    <div
      ref={box}
      role="log"
      aria-label="Journal entries"
      aria-live="off"
      tabIndex={0}
      onScroll={(e) => {
        const el = e.currentTarget
        const bottom = el.scrollHeight - el.scrollTop - el.clientHeight <= 4
        pinned.current = bottom
        if (follow) onPaused(!bottom)
      }}
      className="max-h-[28rem] overflow-auto rounded-md bg-muted p-3 font-mono text-xs"
    >
      <ol>
        {entries.map((e, i) => (
          <li key={`${e.time}-${i}`} className="grid grid-cols-[9ch_8ch_minmax(0,1fr)] gap-x-2 sm:grid-cols-[9ch_8ch_minmax(0,16ch)_1fr]">
            <span>{time(e.time)}</span>
            <span>{e.priority === null ? "" : (PRIORITIES[e.priority] ?? String(e.priority))}</span>
            <span className="truncate" title={e.unit}>
              {e.unit ?? ""}
            </span>
            {/* Below 640 px the message takes its own line, full width. */}
            <span className="col-span-3 break-all whitespace-pre-wrap sm:col-span-1">{e.truncated ? `${e.message}… (truncated)` : e.message}</span>
          </li>
        ))}
      </ol>
    </div>
  )
}

// JournalPanel lists journal entries by unit, priority and time, and follows
// new ones (UI-2, UX-3). unit is controlled so View logs can set it.
export function JournalPanel({ hostId, unit, onUnitChange }: { hostId: string; unit: string; onUnitChange: (unit: string) => void }) {
  const [priority, setPriority] = useState("6")
  const [since, setSince] = useState("1h")
  const [follow, setFollow] = useState(true)
  const [paused, setPaused] = useState(false)
  const [jump, setJump] = useState(0)
  const filters: Filters = { unit, priority, since }
  const followed = useFollow(hostId, filters, follow)
  const page = useQuery({
    queryKey: ["journal", hostId, unit, priority, since],
    queryFn: () => readAction<{ entries: JournalEntry[] }>("services.journal.query", 1, { ...numeric(params(filters, Date.now())) }, hostId),
    enabled: !follow,
  })
  const entries = follow ? followed.entries : [...(page.data?.entries ?? [])].reverse()

  return (
    <section aria-labelledby="journal-heading" className="flex flex-col gap-3">
      <h3 id="journal-heading" className="text-base font-semibold">
        Journal
      </h3>
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <Label htmlFor="journal-unit">Unit</Label>
          <Input id="journal-unit" value={unit} placeholder="Every unit" onChange={(e) => onUnitChange(e.target.value.trim())} />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="journal-priority">Priority</Label>
          <select id="journal-priority" value={priority} onChange={(e) => setPriority(e.target.value)} className="h-9 rounded-md border border-input bg-background px-2 text-sm">
            <option value="">Every priority</option>
            {PRIORITIES.map((p, i) => (
              <option key={p} value={String(i)}>{`${p.charAt(0).toUpperCase()}${p.slice(1)} and more urgent`}</option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="journal-since">Since</Label>
          <select id="journal-since" value={since} onChange={(e) => setSince(e.target.value)} className="h-9 rounded-md border border-input bg-background px-2 text-sm">
            {SINCE.map(([id, label]) => (
              <option key={id} value={id}>
                {label}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-2 pb-2">
          <Switch
            id="journal-follow"
            checked={follow}
            onCheckedChange={(on) => {
              setFollow(on)
              setPaused(false)
            }}
          />
          <Label htmlFor="journal-follow">Follow</Label>
        </div>
      </div>
      {follow && followed.status === "following" && !paused && <StatusPill status="ok" label="Following" />}
      {follow && followed.status === "reconnecting" && <StatusPill status="warn" label="Reconnecting…" live />}
      {follow && followed.status === "ended" && <StatusPill status="off" label="The journal stream ended" />}
      {follow && paused && (
        <Alert role="status">
          <AlertDescription className="flex flex-wrap items-center gap-2">
            Following paused
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setPaused(false)
                setJump((n) => n + 1)
              }}
            >
              <ArrowDown aria-hidden="true" />
              Jump to latest
            </Button>
          </AlertDescription>
        </Alert>
      )}
      {!follow && page.isPending ? (
        <div className="flex flex-col gap-2" role="status" aria-label="Loading the journal">
          <Skeleton className="h-5 w-full" />
          <Skeleton className="h-5 w-full" />
        </div>
      ) : !follow && page.isError ? (
        <Alert variant="destructive" role="alert">
          <AlertDescription className="flex flex-col items-start gap-2">
            <span>{`Couldn't load the journal: ${errorMessage(page.error)}`}</span>
            <Button size="sm" variant="outline" onClick={() => void page.refetch()}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      ) : entries.length === 0 && follow && followed.status === "connecting" ? (
        <div className="flex flex-col gap-2" role="status" aria-label="Connecting to the journal">
          <Skeleton className="h-5 w-full" />
          <Skeleton className="h-5 w-full" />
        </div>
      ) : entries.length === 0 ? (
        <EmptyState title="No journal entries match" description="Widen the priority or the time range." />
      ) : (
        <>
          <Lines key={jump} entries={entries} follow={follow} onPaused={setPaused} />
          {follow && <p className="text-xs text-muted-foreground">{`The last ${MAX_LINES} lines stay in view.`}</p>}
        </>
      )}
    </section>
  )
}

// numeric turns the stream's string parameters into the query's numbers.
function numeric(p: Record<string, string>): Record<string, string | number> {
  const out: Record<string, string | number> = { ...p }
  if (p.priority !== undefined) out.priority = Number(p.priority)
  if (p.since !== undefined) out.since = Number(p.since)
  return out
}
