import { useRef, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Switch } from "darkraise-ui/components/switch"
import { errorMessage, readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { applied } from "@/lib/users"
import { useReopenFocus } from "@/lib/return-focus"

// SshdSettings mirrors users.sshd.get's effective and set values.
export interface SshdSettings {
  passwordAuthentication?: boolean
  kbdInteractiveAuthentication?: boolean
  permitRootLogin?: string
  maxAuthTries?: number
  loginGraceTime?: number
  allowUsers?: string[]
  allowGroups?: string[]
  clientAliveInterval?: number
  clientAliveCountMax?: number
  x11Forwarding?: boolean
}

// SshdEffective mirrors users.sshd.get's effective values, what sshd -T
// reports: another file can set a value the form does not write.
export interface SshdEffective extends Omit<SshdSettings, "permitRootLogin"> {
  permitRootLogin?: "yes" | "prohibit-password" | "forced-commands-only" | "no"
}

export interface SshdState {
  effective: SshdEffective
  set: SshdSettings
  readOnly: string[]
  listsSetBy?: string
  matchIn?: string
}

export const OPEN_A_SESSION = "Open a new SSH session before you confirm"

type Field = keyof SshdSettings
const ROOT_LOGIN: Record<string, string> = { yes: "Allowed", "prohibit-password": "Keys only", no: "Refused", "forced-commands-only": "Forced commands only" }
const NUMBERS: [Field, string, number, number, string][] = [
  ["maxAuthTries", "Login attempts", 1, 10, ""],
  ["loginGraceTime", "Login grace time (seconds)", 10, 600, ""],
  ["clientAliveInterval", "Keep-alive interval (seconds)", 0, 3600, "0 sends none"],
  ["clientAliveCountMax", "Missed keep-alives before closing", 0, 10, ""],
]
const SWITCHES: [Field, string][] = [
  ["passwordAuthentication", "Password login"],
  ["kbdInteractiveAuthentication", "Keyboard-interactive login"],
  ["x11Forwarding", "X11 forwarding"],
]

function onOff(v: boolean | undefined, on: string, off: string): string {
  return v === true ? on : off
}

function list(v: string[] | undefined, none: string): string {
  return v === undefined || v.length === 0 ? none : v.join(" ")
}

function words(text: string): string[] {
  return text.split(/[\s,]+/).filter((w) => w !== "")
}

function same(a: unknown, b: unknown): boolean {
  return JSON.stringify(a ?? null) === JSON.stringify(b ?? null)
}

interface Draft {
  bools: Partial<Record<Field, boolean>>
  numbers: Partial<Record<Field, string>>
  // The number fields the operator typed in; only those are checked and sent.
  edited: Partial<Record<Field, boolean>>
  permitRootLogin: string
  allowUsers: string
  allowGroups: string
}

function draftOf(e: SshdSettings): Draft {
  const bools: Draft["bools"] = {}
  for (const [f] of SWITCHES) bools[f] = e[f] === true
  const numbers: Draft["numbers"] = {}
  for (const [f] of NUMBERS) numbers[f] = e[f] === undefined ? "" : String(e[f] as number)
  return { bools, numbers, edited: {}, permitRootLogin: e.permitRootLogin ?? "prohibit-password", allowUsers: list(e.allowUsers, ""), allowGroups: list(e.allowGroups, "") }
}

function FieldError({ id, children }: { id: string; children: string }) {
  return (
    <p id={id} className="text-sm text-destructive-text">
      {children}
    </p>
  )
}

interface FieldProblem {
  field: Field
  message: string
}

// settingsOf is what 00-darkpanel.conf holds after the change: the values
// Darkpanel set already, plus every value the form changed from the
// effective one; fields another file manages are left out. A number the
// operator did not type in is neither checked nor sent: sshd may run with a
// value outside the form's range that another file set.
function settingsOf(d: Draft, s: SshdState): { settings: SshdSettings; error: FieldProblem | null } {
  const out: Record<string, unknown> = { ...s.set }
  // The daemon refuses a value this file sets where another file manages
  // the field, so the change drops it.
  for (const f of s.readOnly) delete out[f]
  const put = (f: Field, v: unknown) => {
    if (s.readOnly.includes(f)) return
    if (!same(v, s.effective[f]) || f in s.set) out[f] = v
  }
  for (const [f] of SWITCHES) put(f, d.bools[f] === true)
  for (const [f, label, min, max] of NUMBERS) {
    if (d.edited[f] !== true) continue
    const text = (d.numbers[f] ?? "").trim()
    if (text === "" && s.effective[f] === undefined) continue
    const n = Number(text)
    if (!/^[0-9]+$/.test(text) || n < min || n > max) return { settings: {}, error: { field: f, message: `${label}: enter a whole number from ${min} to ${max}` } }
    put(f, n)
  }
  put("permitRootLogin", d.permitRootLogin)
  put("allowUsers", words(d.allowUsers))
  put("allowGroups", words(d.allowGroups))
  for (const f of ["allowUsers", "allowGroups"] as const) {
    if (Array.isArray(out[f]) && (out[f] as string[]).length === 0) delete out[f]
  }
  return { settings: out as SshdSettings, error: null }
}

// HardeningForm stays mounted, hidden, while its change is reviewed, so a
// refused plan returns to it as typed.
function HardeningForm({
  state,
  hostName,
  hidden,
  onCancel,
  onReview,
}: {
  state: SshdState
  hostName: string
  hidden: boolean
  onCancel: () => void
  onReview: (s: SshdSettings) => void
}) {
  const [draft, setDraft] = useState(() => draftOf(state.effective))
  const reviewButton = useReopenFocus(hidden)
  const [error, setError] = useState<FieldProblem | null>(null)
  const listsElsewhere = state.listsSetBy !== undefined && state.listsSetBy !== ""

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    const out = settingsOf(draft, state)
    setError(out.error)
    if (out.error === null) onReview(out.settings)
  }

  return (
    <Dialog open={!hidden} onOpenChange={(open) => (open ? undefined : onCancel())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Edit SSH hardening on ${hostName}`}</DialogTitle>
          <DialogDescription>Starts from the values sshd runs with now.</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          {SWITCHES.map(([f, label]) => (
            <div key={f} className="flex items-center gap-2">
              <Switch id={`sshd-${f}`} checked={draft.bools[f] === true} onCheckedChange={(on) => setDraft((d) => ({ ...d, bools: { ...d.bools, [f]: on } }))} />
              <Label htmlFor={`sshd-${f}`}>{label}</Label>
            </div>
          ))}
          <div className="flex flex-col gap-2">
            <Label htmlFor="sshd-permitRootLogin">Root login</Label>
            <select
              id="sshd-permitRootLogin"
              value={draft.permitRootLogin}
              onChange={(e) => setDraft((d) => ({ ...d, permitRootLogin: e.target.value }))}
              className="h-9 rounded-md border border-input bg-background px-2 text-sm"
            >
              <option value="prohibit-password">Keys only</option>
              <option value="no">Refused</option>
              <option value="yes">Allowed</option>
              {state.effective.permitRootLogin === "forced-commands-only" && <option value="forced-commands-only">Forced commands only</option>}
            </select>
          </div>
          {NUMBERS.map(([f, label, min, max, help]) => (
            <div key={f} className="flex flex-col gap-2">
              <Label htmlFor={`sshd-${f}`}>{label}</Label>
              <Input
                id={`sshd-${f}`}
                inputMode="numeric"
                min={min}
                max={max}
                value={draft.numbers[f] ?? ""}
                aria-invalid={error?.field === f}
                aria-describedby={[error?.field === f ? `sshd-${f}-error` : "", help === "" ? "" : `sshd-${f}-help`].filter((id) => id !== "").join(" ") || undefined}
                onChange={(e) => setDraft((d) => ({ ...d, numbers: { ...d.numbers, [f]: e.target.value }, edited: { ...d.edited, [f]: true } }))}
              />
              {error?.field === f && <FieldError id={`sshd-${f}-error`}>{error.message}</FieldError>}
              {help !== "" && (
                <span id={`sshd-${f}-help`} className="text-xs text-muted-foreground">
                  {help}
                </span>
              )}
            </div>
          ))}
          {(["allowUsers", "allowGroups"] as const).map((f) => (
            <div key={f} className="flex flex-col gap-2">
              <Label htmlFor={`sshd-${f}`}>{f === "allowUsers" ? "Allowed users" : "Allowed groups"}</Label>
              <Input
                id={`sshd-${f}`}
                value={draft[f]}
                readOnly={listsElsewhere}
                placeholder={f === "allowUsers" ? "Everyone" : "Every group"}
                aria-describedby={`sshd-${f}-help`}
                onChange={(e) => setDraft((d) => ({ ...d, [f]: e.target.value }))}
              />
              <span id={`sshd-${f}-help`} className="text-xs text-muted-foreground">
                {listsElsewhere ? `Set in ${state.listsSetBy}; edit that file to change it` : "Names separated by spaces; empty allows everyone"}
              </span>
            </div>
          ))}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button ref={reviewButton} type="submit">Review change</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// SshdCard lists sshd's effective values and, for admins, edits the
// hardening Darkpanel manages (UI-3, UX-5).
export function SshdCard({ hostId, hostName, admin }: { hostId: string; hostName: string; admin: boolean }) {
  const client = useQueryClient()
  const key = ["resource", hostId, "users.sshd.get"]
  const sshd = useQuery({ queryKey: key, queryFn: () => readAction<SshdState>("users.sshd.get", 1, {}, hostId) })
  const [editing, setEditing] = useState(false)
  const [review, setReview] = useState<SshdSettings | null>(null)
  // Whether the reviewed change applied: only then does the form close.
  const done = useRef(false)

  let body
  if (sshd.isPending) {
    body = (
      <div className="flex flex-col gap-2" role="status" aria-label="Loading the SSH daemon">
        <Skeleton className="h-5 w-full" />
        <Skeleton className="h-5 w-2/3" />
      </div>
    )
  } else if (sshd.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't read the SSH daemon: ${errorMessage(sshd.error)}`}</span>
          <Button size="sm" variant="outline" onClick={() => void sshd.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  } else {
    const e = sshd.data.effective
    const elsewhere = sshd.data.listsSetBy !== undefined && sshd.data.listsSetBy !== "" ? ` (set in ${sshd.data.listsSetBy})` : ""
    const rows: [string, string][] = [
      ["Password login", onOff(e.passwordAuthentication, "Allowed", "Refused")],
      ["Keyboard-interactive login", onOff(e.kbdInteractiveAuthentication, "Allowed", "Refused")],
      ["Root login", ROOT_LOGIN[e.permitRootLogin ?? ""] ?? (e.permitRootLogin ?? "")],
      ["Login attempts", String(e.maxAuthTries ?? "")],
      ["Login grace time", e.loginGraceTime === undefined ? "" : `${e.loginGraceTime} s`],
      ["Allowed users", list(e.allowUsers, "Everyone") + elsewhere],
      ["Allowed groups", list(e.allowGroups, "Every group") + elsewhere],
      ["Keep-alive interval", e.clientAliveInterval === undefined || e.clientAliveInterval === 0 ? "Off" : `${e.clientAliveInterval} s`],
      ["X11 forwarding", onOff(e.x11Forwarding, "On", "Off")],
    ]
    body = (
      <>
        <dl className="grid grid-cols-1 gap-x-6 gap-y-1 text-sm sm:grid-cols-[auto_1fr]">
          {rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">{k}</dt>
              <dd className="break-words">{v}</dd>
            </div>
          ))}
        </dl>
        {sshd.data.matchIn !== undefined && sshd.data.matchIn !== "" && (
          <p className="text-sm text-muted-foreground">{`${sshd.data.matchIn} holds a Match block; some users may get other values`}</p>
        )}
        {admin && (
          <div>
            <Button variant="outline" onClick={() => setEditing(true)}>
              Edit hardening
            </Button>
          </div>
        )}
      </>
    )
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>
          <h3 className="text-base font-semibold">SSH daemon</h3>
        </CardTitle>
        <CardDescription>The values sshd runs with now.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">{body}</CardContent>
      {editing && sshd.data !== undefined && (
        <HardeningForm
          state={sshd.data}
          hostName={hostName}
          hidden={review !== null}
          onCancel={() => setEditing(false)}
          onReview={(settings) => {
            done.current = false
            setReview(settings)
          }}
        />
      )}
      {review !== null && (
        <ChangeDialog
          open
          onOpenChange={(open) => {
            if (open) return
            setReview(null)
            if (done.current) setEditing(false)
          }}
          title="Apply SSH hardening"
          note={OPEN_A_SESSION}
          target={{ name: "users.sshd.apply", version: 1, hostId, module: "users", revertible: true }}
          input={{ ...review }}
          onApplied={(c: Change) => {
            void client.invalidateQueries({ queryKey: key })
            if (applied(c)) done.current = true
          }}
        />
      )}
    </Card>
  )
}
