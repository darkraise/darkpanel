import { useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { ShieldCheck, X } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Switch } from "darkraise-ui/components/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { formatTime } from "@/lib/format"
import { applied } from "@/lib/users"
import { useCanOnHost } from "@/session/can"
import { focusableScroller } from "@/lib/table"
import { FALLBACK_FOCUS, useFocusFallback } from "@/lib/return-focus"

interface Jail {
  name: string
  banned: number
  failed: number
  failedTotal: number
}

interface Settings {
  bantimeMinutes: number
  findtimeMinutes: number
  maxRetry: number
  ignoreIp: string[]
  recidive: boolean
}

interface Status {
  installed: boolean
  version?: string
  service: string
  jails: Jail[]
  crowdsec: boolean
  settings?: Settings | null
  ignoreFixed: string[]
}

interface Ban {
  jail: string
  address: string
  start: string
  end?: string | null
}

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
  done: string
}

const IGNORE_RULE = "Enter an IPv4 or IPv6 address or network"

function ipv4(text: string): boolean {
  const parts = text.split(".")
  return parts.length === 4 && parts.every((p) => /^[0-9]{1,3}$/.test(p) && Number(p) <= 255)
}

function ipv6(text: string): boolean {
  if (!/^[0-9a-fA-F:.]+$/.test(text) || !text.includes(":")) return false
  try {
    new URL(`http://[${text}]/`)
    return true
  } catch {
    return false
  }
}

// ignoreOK says whether text is an address or a network fail2ban's
// ignoreip takes: IPv4 or IPv6, with an optional prefix length.
function ignoreOK(text: string): boolean {
  const [addr = "", prefix, extra] = text.split("/")
  if (extra !== undefined) return false
  const v4 = ipv4(addr)
  if (!v4 && !ipv6(addr)) return false
  if (prefix === undefined) return true
  return /^[0-9]{1,3}$/.test(prefix) && Number(prefix) <= (v4 ? 32 : 128)
}

function Loading() {
  return (
    <div className="flex flex-col gap-2" role="status" aria-label="Loading intrusion prevention">
      <Skeleton className="h-24 w-full" />
      <Skeleton className="h-8 w-full" />
    </div>
  )
}

function JailsCard({ jails, service }: { jails: Jail[]; service: string }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h3 className="text-base font-semibold">Jails</h3>
        </CardTitle>
        <CardDescription>{service === "active" ? "fail2ban is running." : `fail2ban is ${service === "" ? "stopped" : service}.`}</CardDescription>
      </CardHeader>
      <CardContent>
        {jails.length === 0 ? (
          <p className="text-sm text-muted-foreground">No jail is running</p>
        ) : (
          <ul className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            {jails.map((j) => (
              <li key={j.name} className="flex flex-col gap-1">
                <span className="font-mono text-sm">{j.name}</span>
                <span className="text-sm">{`${j.banned} banned now · ${j.failed} failing now · ${j.failedTotal} failed logins in total`}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}

function BansTable({ hostId, operate, onUnban }: { hostId: string; operate: boolean; onUnban: (b: Ban) => void }) {
  const bans = useQuery({
    queryKey: ["resource", hostId, "fail2ban.ban.list"],
    queryFn: () => readAction<{ bans: Ban[] }>("fail2ban.ban.list", 1, {}, hostId),
  })
  let body
  if (bans.isPending) body = <Skeleton className="h-8 w-full" role="status" aria-label="Loading the bans" />
  else if (bans.isError)
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>Couldn't load the bans</span>
          <Button size="sm" variant="outline" onClick={() => void bans.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  else if (bans.data.bans.length === 0) body = <p className="text-sm text-muted-foreground">No address is banned</p>
  else
    body = (
      <div className="overflow-x-auto">
        <Table aria-label="Banned addresses" ref={focusableScroller("Banned addresses table")}>
          <TableHeader>
            <TableRow>
              <TableHead>Address</TableHead>
              <TableHead>Jail</TableHead>
              <TableHead>Since</TableHead>
              <TableHead>Until</TableHead>
              {operate && (
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {bans.data.bans.map((b) => (
              <TableRow key={`${b.jail} ${b.address}`}>
                <TableCell className="font-mono text-xs break-all">{b.address}</TableCell>
                <TableCell>{b.jail}</TableCell>
                <TableCell className="whitespace-nowrap">{formatTime(b.start)}</TableCell>
                <TableCell className="whitespace-nowrap">{b.end === undefined || b.end === null ? "Until unbanned" : formatTime(b.end)}</TableCell>
                {operate && (
                  <TableCell>
                    <Button size="sm" variant="outline" aria-label={`Unban ${b.address} from ${b.jail}`} onClick={() => onUnban(b)}>
                      Unban
                    </Button>
                  </TableCell>
                )}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    )
  return (
    <section aria-labelledby="bans-heading" className="flex flex-col gap-3">
      {/* Takes focus when an unban removed the row whose button opened it. */}
      <h3 id="bans-heading" tabIndex={-1} {...{ [FALLBACK_FOCUS]: "" }} className="text-base font-semibold outline-none">
        Banned addresses
      </h3>
      {body}
    </section>
  )
}

type NumberField = "bantimeMinutes" | "findtimeMinutes" | "maxRetry"

const NUMBERS: [NumberField, string, number, number][] = [
  ["bantimeMinutes", "Ban for (minutes)", 1, 43200],
  ["findtimeMinutes", "Count failures over (minutes)", 1, 1440],
  ["maxRetry", "Failures before a ban", 1, 20],
]

function SettingsCard({ settings, fixed, onReview }: { settings: Settings; fixed: string[]; onReview: (p: Pending) => void }) {
  const [numbers, setNumbers] = useState<Record<string, string>>(() => Object.fromEntries(NUMBERS.map(([f]) => [f, String(settings[f])])))
  const [own, setOwn] = useState(() => settings.ignoreIp.filter((ip) => !fixed.includes(ip)))
  const [recidive, setRecidive] = useState(settings.recidive)
  const [entry, setEntry] = useState("")
  const [entryError, setEntryError] = useState<string | null>(null)
  const [error, setError] = useState<{ field: NumberField; message: string } | null>(null)

  function add() {
    const text = entry.trim()
    if (!ignoreOK(text)) {
      setEntryError(IGNORE_RULE)
      return
    }
    setEntryError(null)
    setEntry("")
    if (!own.includes(text) && !fixed.includes(text)) setOwn((o) => [...o, text])
  }

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    // An address typed but not yet added goes with the change, or stops it.
    const typed = entry.trim()
    if (typed !== "" && !ignoreOK(typed)) {
      setEntryError(IGNORE_RULE)
      return
    }
    const mine = typed === "" || own.includes(typed) || fixed.includes(typed) ? own : [...own, typed]
    if (mine !== own) {
      setOwn(mine)
      setEntry("")
    }
    const out: Record<string, unknown> = { ignoreIp: [...fixed, ...mine], recidive }
    for (const [f, label, min, max] of NUMBERS) {
      const text = (numbers[f] ?? "").trim()
      if (!/^[0-9]+$/.test(text) || Number(text) < min || Number(text) > max) {
        setError({ field: f, message: `${label}: enter a whole number from ${min} to ${max}` })
        return
      }
      out[f] = Number(text)
    }
    setError(null)
    onReview({ action: "fail2ban.settings.apply", title: "Change fail2ban's settings", input: out, done: "fail2ban settings saved" })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h3 className="text-base font-semibold">Settings</h3>
        </CardTitle>
      </CardHeader>
      <CardContent>
        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            {NUMBERS.map(([f, label, min, max]) => (
              <div key={f} className="flex flex-col gap-1">
                <Label htmlFor={`f2b-${f}`}>{label}</Label>
                <Input
                  id={`f2b-${f}`}
                  inputMode="numeric"
                  min={min}
                  max={max}
                  value={numbers[f] ?? ""}
                  aria-invalid={error?.field === f}
                  aria-describedby={error?.field === f ? `f2b-${f}-error` : undefined}
                  onChange={(e) => setNumbers((n) => ({ ...n, [f]: e.target.value }))}
                />
                {error?.field === f && (
                  <p id={`f2b-${f}-error`} className="text-sm text-destructive-text">
                    {error.message}
                  </p>
                )}
              </div>
            ))}
          </div>
          <div className="flex items-center gap-2">
            <Switch id="f2b-recidive" checked={recidive} onCheckedChange={setRecidive} />
            <Label htmlFor="f2b-recidive">Ban repeat offenders for a week</Label>
          </div>
          <div className="flex flex-col gap-2">
            <span id="f2b-ignore-heading" className="text-sm font-medium">
              Never banned
            </span>
            <ul aria-labelledby="f2b-ignore-heading" className="flex flex-wrap gap-1">
              {fixed.map((ip) => (
                <li key={ip}>
                  <Badge variant="outline" className="font-mono" title="Always kept: loopback, the controller and the tailnet">
                    {ip}
                  </Badge>
                </li>
              ))}
              {own.map((ip) => (
                <li key={ip}>
                  <Badge variant="secondary" className="gap-1 font-mono">
                    {ip}
                    <button type="button" aria-label={`Remove ${ip}`} className="rounded-sm" onClick={() => setOwn((o) => o.filter((x) => x !== ip))}>
                      <X aria-hidden="true" className="size-3" />
                    </button>
                  </Badge>
                </li>
              ))}
            </ul>
            <span className="text-xs text-muted-foreground">Outlined entries are always kept: loopback, the controller and the tailnet.</span>
          </div>
          <div className="flex flex-col gap-1">
            <Label htmlFor="f2b-ignore">Add an address or network</Label>
            <div className="flex gap-2">
              <Input
                id="f2b-ignore"
                value={entry}
                placeholder="203.0.113.0/24"
                aria-invalid={entryError !== null}
                aria-describedby={entryError !== null ? "f2b-ignore-error" : undefined}
                onChange={(e) => {
                  setEntry(e.target.value)
                  setEntryError(null)
                }}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    e.preventDefault()
                    add()
                  }
                }}
              />
              <Button type="button" variant="outline" onClick={add}>
                Add
              </Button>
            </div>
            {entryError !== null && (
              <p id="f2b-ignore-error" className="text-sm text-destructive-text">
                {entryError}
              </p>
            )}
          </div>
          <div>
            <Button type="submit">Review change</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  )
}

// Fail2banPage shows fail2ban's jails and bans, unbans for operators, and
// installs and configures it for admins (UI-4, UX-6).
export function Fail2banPage({ hostId }: { hostId: string }) {
  const operate = useCanOnHost("operate")
  const admin = useCanOnHost("admin")
  const client = useQueryClient()
  const key = ["resource", hostId, "fail2ban.status.get"]
  const status = useQuery({ queryKey: key, queryFn: () => readAction<Status>("fail2ban.status.get", 1, {}, hostId) })
  const [pending, setPending] = useState<Pending | null>(null)
  useFocusFallback(pending !== null)

  let body
  if (status.isPending) body = <Loading />
  else if (status.isError)
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>Couldn't load intrusion prevention</span>
          <Button size="sm" variant="outline" onClick={() => void status.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  else {
    const s = status.data
    body = (
      <>
        {s.crowdsec && (
          <Alert role="status">
            <ShieldCheck aria-hidden="true" />
            <AlertTitle as="h3">CrowdSec runs on this host</AlertTitle>
            <AlertDescription>Darkpanel shows its fail2ban only; manage CrowdSec with its own tools.</AlertDescription>
          </Alert>
        )}
        {!s.installed ? (
          <Card>
            <CardHeader>
              <CardTitle>
                <h3 className="text-base font-semibold">fail2ban is not installed</h3>
              </CardTitle>
              <CardDescription>It bans addresses that keep failing to log in over SSH.</CardDescription>
            </CardHeader>
            {admin && !s.crowdsec && (
              <CardContent>
                <Button onClick={() => setPending({ action: "fail2ban.package.install", title: "Install fail2ban", input: {}, done: "fail2ban installed" })}>
                  Install fail2ban
                </Button>
              </CardContent>
            )}
          </Card>
        ) : (
          <>
            <JailsCard jails={s.jails} service={s.service} />
            <BansTable
              hostId={hostId}
              operate={operate}
              onUnban={(b) => setPending({ action: "fail2ban.ban.remove", title: `Unban ${b.address}`, input: { jail: b.jail, address: b.address }, done: `${b.address} unbanned` })}
            />
            {admin && s.settings !== undefined && s.settings !== null && <SettingsCard settings={s.settings} fixed={s.ignoreFixed} onReview={setPending} />}
          </>
        )}
      </>
    )
  }

  return (
    <div className="flex flex-col gap-6">
      <h2 className="sr-only">Intrusion prevention</h2>
      {body}
      {pending !== null && (
        <ChangeDialog
          open
          onOpenChange={(open) => (open ? undefined : setPending(null))}
          title={pending.title}
          target={{ name: pending.action, version: 1, hostId, module: "fail2ban" }}
          input={pending.input}
          onApplied={(c: Change) => {
            void client.invalidateQueries({ queryKey: key })
            void client.invalidateQueries({ queryKey: ["resource", hostId, "fail2ban.ban.list"] })
            if (applied(c)) toast(pending.done)
          }}
        />
      )}
    </div>
  )
}
