import { useLayoutEffect, type ReactNode } from "react"
import { useSearchParams } from "react-router"
import { ArrowDown, ChevronRight, FolderPlus, MoreHorizontal, RotateCw, Search, Upload, X } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Progress } from "darkraise-ui/components/progress"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Switch } from "darkraise-ui/components/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Tabs, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { Textarea } from "darkraise-ui/components/textarea"
import { StatusPill } from "@/components/visual/status-pill"
import type { Status } from "@/components/visual/status"
import * as fx from "@/modules/m11-fixtures"

// The M11 boards (spec UI-1 to UI-8): every tab and state the wave adds, drawn
// from kit components with the M11 fixtures, so each can be judged against
// the UI/UX contract before the pages are built. Dialogs, sheets and menus
// are drawn open in place, as frames, so several show at once. A worst case
// shows its first rows and says how many the list holds.

export type M11BoardId = "services" | "journal" | "users" | "fail2ban" | "console" | "jobs" | "tuning"

const SAMPLE = 6

function Tag({ ids }: { ids: string }) {
  return <span className="font-mono text-xs text-muted-foreground">{ids}</span>
}

function State({ id, heading, children }: { id: string; heading: string; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-4 rounded-lg border border-border p-4">
      <h2 id={id} className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
        {heading}
      </h2>
      {children}
    </section>
  )
}

function Frames({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-1 gap-3 md:grid-cols-2">{children}</div>
}

function Frame({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-3 rounded-md border border-dashed border-border p-3">
      <Tag ids={label} />
      {children}
    </div>
  )
}

// DialogFrame draws an open dialog in place.
function DialogFrame({ title, description, children }: { title: string; description?: string; children: ReactNode }) {
  return (
    <div role="group" aria-label={title} className="flex max-w-lg flex-col gap-4 rounded-lg border border-border bg-card p-5 shadow-md">
      <div className="flex flex-col gap-1">
        <h3 className="text-base font-semibold">{title}</h3>
        {description !== undefined && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {children}
    </div>
  )
}

// MenuFrame draws an open row menu; a disabled item carries its reason.
function MenuFrame({ items }: { items: { label: string; reason?: string }[] }) {
  return (
    <div role="group" aria-label="Row menu" className="flex w-64 flex-col rounded-md border border-border bg-popover p-1 text-sm shadow-md">
      {items.map((item) => (
        <div key={item.label} className={item.reason === undefined ? "rounded px-2 py-1.5" : "rounded px-2 py-1.5 text-muted-foreground"}>
          {item.label}
          {item.reason !== undefined && <span className="block text-xs">{item.reason}</span>}
        </div>
      ))}
    </div>
  )
}

function RowMenu({ name }: { name: string }) {
  return (
    <Button size="icon" variant="ghost" aria-label={`Actions for ${name}`}>
      <MoreHorizontal aria-hidden="true" />
    </Button>
  )
}

function Count({ shown, total, thing }: { shown: number; total: number; thing: string }) {
  return <p className="text-xs text-muted-foreground">{`Showing ${shown} of ${total} ${thing}`}</p>
}

function Truncated({ text }: { text: string }) {
  return (
    <span className="block max-w-[28ch] truncate" title={text}>
      {text}
    </span>
  )
}

function FieldError({ id, children }: { id: string; children: ReactNode }) {
  return (
    <p id={id} className="text-sm text-destructive-text">
      {children}
    </p>
  )
}

// ---- UI-1 Services

const UNIT_STATUS: Record<string, [Status, string]> = {
  running: ["ok", "Running"],
  exited: ["info", "Exited"],
  failed: ["error", "Failed"],
  dead: ["off", "Inactive"],
  waiting: ["info", "Waiting"],
}

function UnitsTable({ units, total }: { units: fx.M11Unit[]; total?: number }) {
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Unit</TableHead>
            <TableHead>Description</TableHead>
            <TableHead>State</TableHead>
            <TableHead>At boot</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {units.map((u) => {
            const [status, label] = UNIT_STATUS[u.sub] ?? ["off", u.sub]
            return (
              <TableRow key={u.name}>
                <TableCell className="font-mono text-xs">
                  <Truncated text={u.name} />
                </TableCell>
                <TableCell>
                  <Truncated text={u.description} />
                </TableCell>
                <TableCell>
                  <StatusPill status={status} label={label} />
                </TableCell>
                <TableCell>{u.enabled === "enabled" ? "Enabled" : u.enabled === "static" ? "Static" : "Disabled"}</TableCell>
                <TableCell>
                  <RowMenu name={u.name} />
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      {total !== undefined && <Count shown={units.length} total={total} thing="units" />}
    </>
  )
}

function ServicesToolbar({ search = "", filter = "All states" }: { search?: string; filter?: string }) {
  return (
    <div className="flex flex-wrap items-end gap-3">
      <div className="flex flex-col gap-1">
        <Label htmlFor={`svc-search-${search}`}>Search units</Label>
        <Input id={`svc-search-${search}`} readOnly value={search} placeholder="Name or description" prefix={<Search aria-hidden="true" />} />
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-sm font-medium">State</span>
        <Button variant="outline">{filter}</Button>
      </div>
    </div>
  )
}

function ServicesBoard() {
  const failed = fx.unitsDefault.filter((u) => u.active === "failed")
  return (
    <>
      <State id="m11-services-default" heading="UI-1 Services: default">
        <ServicesToolbar />
        <UnitsTable units={fx.unitsDefault} />
      </State>
      <State id="m11-services-failed" heading="UI-1 Services: failed units">
        <ServicesToolbar filter="Failed" />
        <UnitsTable units={failed} />
      </State>
      <State id="m11-services-states" heading="UI-1 Services: loading, error, filtered empty">
        <Frames>
          <Frame label="UX-2 loading">
            <div className="flex flex-col gap-2" role="status" aria-label="Loading the services">
              <Skeleton className="h-6 w-full" />
              <Skeleton className="h-6 w-full" />
              <Skeleton className="h-6 w-2/3" />
            </div>
          </Frame>
          <Frame label="UX-2 error">
            <Alert variant="destructive">
              <AlertDescription className="flex flex-col items-start gap-2">
                <span>Couldn't load services</span>
                <Button size="sm" variant="outline">
                  Retry
                </Button>
              </AlertDescription>
            </Alert>
          </Frame>
          <Frame label="UX-2 filtered empty">
            <ServicesToolbar search="postgres-replica" />
            <EmptyState title="No units match 'postgres-replica'" action={<Button variant="outline">Clear filter</Button>} />
          </Frame>
        </Frames>
      </State>
      <State id="m11-services-worst" heading="UI-1 Services: worst case">
        <UnitsTable units={fx.unitsWorst.slice(0, SAMPLE)} total={fx.unitsWorst.length} />
      </State>
      <State id="m11-services-menu" heading="UI-1 Services: protected unit menu">
        <Frames>
          <Frame label="UX-2 ssh.service, protected: the change asks its name">
            <MenuFrame items={[{ label: "Start" }, { label: "Stop" }, { label: "Restart" }, { label: "Reload" }, { label: "Enable" }, { label: "Disable" }, { label: "View logs" }]} />
            <DialogFrame title="Stop ssh.service on web-1" description="SSH sessions to this host end and new logins are refused.">
              <div className="flex flex-col gap-2">
                <Label htmlFor="svc-confirm">
                  Type <code className="font-mono">ssh.service</code> to confirm
                </Label>
                <Input id="svc-confirm" readOnly value="" />
              </div>
              <div className="flex justify-end gap-2">
                <Button variant="outline">Cancel</Button>
                <Button variant="destructive" disabled>
                  Stop
                </Button>
              </div>
            </DialogFrame>
          </Frame>
          <Frame label="UX-2 darkpanel-agent.service: Darkpanel's own unit">
            <MenuFrame
              items={[
                { label: "Start", reason: "Darkpanel manages its own units" },
                { label: "Stop", reason: "Darkpanel manages its own units" },
                { label: "Restart", reason: "Darkpanel manages its own units" },
                { label: "View logs" },
              ]}
            />
          </Frame>
        </Frames>
      </State>
    </>
  )
}

// ---- UI-2 Journal

const PRIORITY = ["emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"]

function JournalLines({ entries }: { entries: fx.M11JournalEntry[] }) {
  return (
    <ol className="flex max-h-72 flex-col overflow-hidden rounded-md border border-border bg-muted/30 p-2 font-mono text-xs" aria-label="Journal entries">
      {entries.map((e) => (
        <li key={e.time} className="grid grid-cols-[9ch_7ch_16ch_1fr] gap-2">
          <span>{new Date(e.time / 1000).toISOString().slice(11, 19)}</span>
          <span>{e.priority === null ? "" : PRIORITY[e.priority]}</span>
          <span className="truncate">{e.unit}</span>
          <span className="truncate">{e.truncated ? `${e.message.slice(0, 60)}… (truncated)` : e.message}</span>
        </li>
      ))}
    </ol>
  )
}

function JournalFilters({ following }: { following: boolean }) {
  return (
    <div className="flex flex-wrap items-end gap-3">
      <div className="flex flex-col gap-1">
        <Label htmlFor={`j-unit-${String(following)}`}>Unit</Label>
        <Input id={`j-unit-${String(following)}`} readOnly value="nginx.service" />
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-sm font-medium">Priority</span>
        <Button variant="outline">Info and more urgent</Button>
      </div>
      <div className="flex flex-col gap-1">
        <span className="text-sm font-medium">Since</span>
        <Button variant="outline">Last hour</Button>
      </div>
      <div className="flex items-center gap-2 pb-2">
        <Switch id={`j-follow-${String(following)}`} checked={following} aria-label="Follow" />
        <Label htmlFor={`j-follow-${String(following)}`}>Follow</Label>
      </div>
    </div>
  )
}

function JournalBoard() {
  const recent = fx.journalWorst.entries.slice(-12)
  return (
    <>
      <State id="m11-journal-following" heading="UI-2 Journal: following">
        <JournalFilters following />
        <StatusPill status="ok" label="Following" />
        <JournalLines entries={recent} />
        <p className="text-xs text-muted-foreground">The last 2000 lines stay in view.</p>
      </State>
      <State id="m11-journal-paused" heading="UI-2 Journal: paused">
        <JournalFilters following />
        <Alert role="status">
          <AlertDescription className="flex flex-wrap items-center gap-2">
            Following paused
            <Button size="sm" variant="outline">
              <ArrowDown aria-hidden="true" />
              Jump to latest
            </Button>
          </AlertDescription>
        </Alert>
        <JournalLines entries={fx.journalWorst.entries.slice(0, 8)} />
      </State>
      <State id="m11-journal-reconnecting" heading="UI-2 Journal: reconnecting">
        <JournalFilters following />
        <StatusPill status="warn" label="Reconnecting…" />
        <JournalLines entries={recent.slice(0, 6)} />
      </State>
      <State id="m11-journal-empty" heading="UI-2 Journal: empty">
        <JournalFilters following={false} />
        <EmptyState title="No journal entries match" description="Widen the priority or the time range." />
      </State>
    </>
  )
}

// ---- UI-3 Users and SSH

function AccountsTable({ accounts, total }: { accounts: fx.M11Account[]; total?: number }) {
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Access</TableHead>
            <TableHead>Password</TableHead>
            <TableHead>Keys</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.map((a) => (
            <TableRow key={a.name}>
              <TableCell className="font-mono text-xs">
                <Truncated text={a.name} />
              </TableCell>
              <TableCell className="flex flex-wrap gap-1">
                {a.sudo && <Badge variant="outline">Sudo</Badge>}
                {a.locked && <Badge variant="outline">Locked</Badge>}
              </TableCell>
              <TableCell>{a.passwordSet ? "Set" : "None"}</TableCell>
              <TableCell>{a.keys}</TableCell>
              <TableCell>
                <RowMenu name={a.name} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {total !== undefined && <Count shown={accounts.length} total={total} thing="accounts" />}
    </>
  )
}

function SshdCard() {
  const e = fx.sshdState.effective
  const rows: [string, string][] = [
    ["Password login", e.passwordAuthentication ? "Allowed" : "Refused"],
    ["Root login", "Keys only"],
    ["Login attempts", String(e.maxAuthTries)],
    ["Login grace time", `${e.loginGraceTime} s`],
    ["Allowed users", "Everyone"],
    ["X11 forwarding", e.x11Forwarding ? "On" : "Off"],
  ]
  return (
    <Card>
      <CardHeader>
        <CardTitle>SSH daemon</CardTitle>
        <CardDescription>The values sshd runs with now.</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <dl className="grid grid-cols-2 gap-x-6 gap-y-1 text-sm">
          {rows.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="text-muted-foreground">{k}</dt>
              <dd>{v}</dd>
            </div>
          ))}
        </dl>
        <div>
          <Button variant="outline">Edit hardening</Button>
        </div>
      </CardContent>
    </Card>
  )
}

function UsersBoard() {
  return (
    <>
      <State id="m11-users-default" heading="UI-3 Users and SSH: default">
        <div className="flex justify-end">
          <Button>Add user</Button>
        </div>
        <AccountsTable accounts={fx.accountsDefault} />
        <SshdCard />
        <Count shown={SAMPLE} total={fx.accountsWorst.length} thing="accounts in the worst case" />
      </State>
      <State id="m11-users-add" heading="UI-3 Users and SSH: add user dialog">
        <DialogFrame title="Add a user on web-1">
          <div className="flex flex-col gap-2">
            <Label htmlFor="u-name">Name</Label>
            <Input id="u-name" readOnly value="Deploy" aria-invalid aria-describedby="u-name-error" />
            <FieldError id="u-name-error">Use lowercase letters, digits, - and _, starting with a letter</FieldError>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="u-pass">Password</Label>
            <Input id="u-pass" type="password" readOnly value="" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="u-pass2">Confirm the password</Label>
            <Input id="u-pass2" type="password" readOnly value="" />
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="u-sudo" />
            <Label htmlFor="u-sudo">Can use sudo</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline">Cancel</Button>
            <Button>Review change</Button>
          </div>
        </DialogFrame>
      </State>
      <State id="m11-users-sheet" heading="UI-3 Users and SSH: user sheet">
        <div role="group" aria-label="User deploy" className="ml-auto flex w-full max-w-md flex-col gap-4 rounded-lg border border-border bg-card p-5 shadow-md">
          <h3 className="text-base font-semibold">deploy</h3>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Type</TableHead>
                <TableHead>Fingerprint</TableHead>
                <TableHead>Comment</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {fx.keysDeploy.keys.map((k) => (
                <TableRow key={k.fingerprint}>
                  <TableCell>{`${k.type.replace("ssh-", "").toUpperCase()} ${k.bits}`}</TableCell>
                  <TableCell className="font-mono text-xs">
                    <Truncated text={k.fingerprint} />
                  </TableCell>
                  <TableCell>
                    <Truncated text={k.comment} />
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <div className="flex flex-col gap-2">
            <Label htmlFor="u-key">Add a public key</Label>
            <Textarea id="u-key" readOnly rows={3} value="ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7 deploy@old" aria-invalid aria-describedby="u-key-error" />
            <FieldError id="u-key-error">RSA keys need at least 3072 bits</FieldError>
          </div>
          <div>
            <Button>Add key</Button>
          </div>
        </div>
      </State>
      <State id="m11-users-hardening" heading="UI-3 Users and SSH: hardening form with guard error">
        <DialogFrame title="Edit SSH hardening on web-1" description="Starts from the values sshd runs with now.">
          <div className="flex items-center gap-2">
            <Switch id="h-pass" checked={false} aria-label="Password login" />
            <Label htmlFor="h-pass">Password login</Label>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="h-allow">Allowed users</Label>
            <Input id="h-allow" readOnly value="deploy" />
          </div>
          <Alert variant="destructive" role="alert">
            <AlertTitle>No account could log in after this change</AlertTitle>
            <AlertDescription>Considered: root (no password login), deploy (no key), alice (locked), backup (no shell).</AlertDescription>
          </Alert>
          <div className="flex items-center gap-2">
            <Checkbox id="h-override" />
            <Label htmlFor="h-override">Apply anyway; I have another way in</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline">Cancel</Button>
            <Button>Review change</Button>
          </div>
        </DialogFrame>
      </State>
    </>
  )
}

// ---- UI-4 Intrusion prevention

function JailsCard() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Jails</CardTitle>
      </CardHeader>
      <CardContent className="grid grid-cols-2 gap-4">
        {fx.fail2banInstalled.jails.map((j) => (
          <div key={j.name} className="flex flex-col gap-1">
            <span className="font-mono text-sm">{j.name}</span>
            <span className="text-sm">{`${j.banned} banned now · ${j.failedTotal} failed logins in total`}</span>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}

function BansTable({ bans, total }: { bans: fx.M11Ban[]; total?: number }) {
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Address</TableHead>
            <TableHead>Jail</TableHead>
            <TableHead>Since</TableHead>
            <TableHead>Until</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {bans.map((b) => (
            <TableRow key={`${b.jail}-${b.address}`}>
              <TableCell className="font-mono text-xs">{b.address}</TableCell>
              <TableCell>{b.jail}</TableCell>
              <TableCell>{b.start.slice(11, 16)}</TableCell>
              <TableCell>{b.end === null ? "Until unbanned" : b.end.slice(5, 16).replace("T", " ")}</TableCell>
              <TableCell>
                <Button size="sm" variant="outline">
                  Unban
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {total !== undefined && <Count shown={bans.length} total={total} thing="bans" />}
    </>
  )
}

function Fail2banSettings() {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Settings</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <div className="grid grid-cols-3 gap-3">
          {[
            ["f-ban", "Ban for (minutes)", "10"],
            ["f-find", "Count failures over (minutes)", "10"],
            ["f-retry", "Failures before a ban", "5"],
          ].map(([id, label, value]) => (
            <div key={id} className="flex flex-col gap-1">
              <Label htmlFor={id}>{label}</Label>
              <Input id={id} readOnly value={value} />
            </div>
          ))}
        </div>
        <span className="text-sm font-medium">Never banned</span>
        <div className="flex flex-wrap gap-1">
          {fx.fail2banInstalled.ignoreFixed.map((ip) => (
            <Badge key={ip} variant="outline" className="font-mono">
              {ip}
            </Badge>
          ))}
          <Badge variant="secondary" className="font-mono">
            198.51.100.0/24
          </Badge>
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="f-ignore">Add an address or network</Label>
          <Input id="f-ignore" readOnly value="10.0.0.300" aria-invalid aria-describedby="f-ignore-error" />
          <FieldError id="f-ignore-error">Enter an IPv4 or IPv6 address or network</FieldError>
        </div>
      </CardContent>
    </Card>
  )
}

function Fail2banBoard() {
  return (
    <>
      <State id="m11-f2b-none" heading="UI-4 Intrusion prevention: not installed">
        <Card>
          <CardHeader>
            <CardTitle>fail2ban is not installed</CardTitle>
            <CardDescription>It bans addresses that keep failing to log in over SSH.</CardDescription>
          </CardHeader>
          <CardContent>
            <Button>Install fail2ban</Button>
          </CardContent>
        </Card>
      </State>
      <State id="m11-f2b-installed" heading="UI-4 Intrusion prevention: installed">
        <JailsCard />
        <BansTable bans={fx.bansDefault} />
        <Fail2banSettings />
      </State>
      <State id="m11-f2b-crowdsec" heading="UI-4 Intrusion prevention: CrowdSec present">
        <Alert role="status">
          <AlertTitle>CrowdSec runs on this host</AlertTitle>
          <AlertDescription>Darkpanel shows its fail2ban only; manage CrowdSec with its own tools.</AlertDescription>
        </Alert>
        <JailsCard />
      </State>
      <State id="m11-f2b-worst" heading="UI-4 Intrusion prevention: worst case">
        <BansTable bans={fx.bansWorst.slice(0, SAMPLE)} total={fx.bansWorst.length} />
      </State>
    </>
  )
}

// ---- UI-5 and UI-6 Console

function ConsoleTabs({ value }: { value: "terminal" | "files" }) {
  return (
    <Tabs value={value}>
      <TabsList aria-label="Console">
        <TabsTrigger value="terminal">Terminal</TabsTrigger>
        <TabsTrigger value="files">Files</TabsTrigger>
      </TabsList>
    </Tabs>
  )
}

function TerminalToolbar({ state, status }: { state: string; status: Status }) {
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-t-md border border-border px-3 py-2">
      <span className="font-medium">web-1</span>
      <StatusPill status={status} label={state} />
      <span className="text-xs text-muted-foreground">Ctrl+Shift+L moves focus here</span>
      <div className="ml-auto flex gap-2">
        <Button size="sm" variant="outline">
          <RotateCw aria-hidden="true" />
          Reconnect
        </Button>
        <Button size="sm" variant="outline">
          <X aria-hidden="true" />
          Close
        </Button>
      </div>
    </div>
  )
}

function TerminalArea({ lines }: { lines: string[] }) {
  return (
    <pre aria-label="Terminal" className="-mt-4 h-48 overflow-hidden rounded-b-md border border-t-0 border-border bg-muted/30 p-3 font-mono text-xs">
      {lines.join("\n")}
    </pre>
  )
}

function FilesTable({ entries, total }: { entries: fx.M11FileInfo[]; total?: number }) {
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Size</TableHead>
            <TableHead>Mode</TableHead>
            <TableHead>Owner</TableHead>
            <TableHead>Modified</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {entries.map((f) => (
            <TableRow key={f.name}>
              <TableCell className="font-mono text-xs">
                <Truncated text={f.type === "dir" ? `${f.name}/` : f.type === "symlink" ? `${f.name} → ${f.target ?? ""}` : f.name} />
                {f.protected !== undefined && <span className="block text-xs text-muted-foreground">{f.protected}</span>}
              </TableCell>
              <TableCell>{f.type === "dir" ? "" : f.size < 1024 ? `${f.size} B` : `${Math.round(f.size / 1024)} KiB`}</TableCell>
              <TableCell className="font-mono text-xs">{f.mode}</TableCell>
              <TableCell>{`${f.owner}:${f.group}`}</TableCell>
              <TableCell>{f.modified.slice(0, 16).replace("T", " ")}</TableCell>
              <TableCell>
                <RowMenu name={f.name} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {total !== undefined && (
        <div className="flex items-center gap-3">
          <Count shown={entries.length} total={total} thing="entries on this page" />
          <Button size="sm" variant="outline">
            Show more
          </Button>
        </div>
      )}
    </>
  )
}

function PathBar({ path }: { path: string }) {
  const parts = path.split("/").filter((p) => p !== "")
  return (
    <div className="flex flex-wrap items-end gap-3">
      <nav aria-label="Path" className="flex items-center gap-1 text-sm">
        <span>/</span>
        {parts.map((p) => (
          <span key={p} className="flex items-center gap-1">
            <ChevronRight className="size-3" aria-hidden="true" />
            {p}
          </span>
        ))}
      </nav>
      <div className="flex flex-col gap-1">
        <Label htmlFor={`path-${path}`}>Path</Label>
        <Input id={`path-${path}`} readOnly value={path} className="font-mono" />
      </div>
      <div className="ml-auto flex gap-2">
        <Button variant="outline">
          <Upload aria-hidden="true" />
          Upload
        </Button>
        <Button variant="outline">
          <FolderPlus aria-hidden="true" />
          New folder
        </Button>
      </div>
    </div>
  )
}

function ConsoleBoard() {
  return (
    <>
      <State id="m11-console-off" heading="UI-5 Console: off">
        <EmptyState title="Off on this host" description="Turning it on lets admins open a root terminal and manage files on this host." action={<Button>Enable</Button>} />
      </State>
      <State id="m11-console-connected" heading="UI-5 Console: terminal connected">
        <ConsoleTabs value="terminal" />
        <TerminalToolbar state="Connected" status="ok" />
        <TerminalArea lines={["root@web-1:~# systemctl is-active nginx", "active", "root@web-1:~# _"]} />
        <div className="flex flex-wrap gap-2" role="group" aria-label="Keys (below 768 px)">
          {["Esc", "Tab", "Ctrl", "←", "↑", "↓", "→"].map((k) => (
            <Button key={k} size="sm" variant="outline">
              {k}
            </Button>
          ))}
        </div>
      </State>
      <State id="m11-console-closed" heading="UI-5 Console: terminal closed">
        <TerminalToolbar state="Closed: idle for 30 minutes" status="off" />
        <TerminalArea lines={["root@web-1:~# ", "", "[The terminal closed: idle for 30 minutes]"]} />
        <p className="text-xs text-muted-foreground">Also: session expired, session limit, host offline, agent restarted, agent too old</p>
      </State>
      <State id="m11-console-stepup" heading="UI-6 Console: step-up dialog">
        <DialogFrame title="Enter your authenticator code" description="Opening a terminal needs your authenticator code">
          <div className="flex flex-col gap-2">
            <Label htmlFor="board-step-up">Authenticator code</Label>
            <Input id="board-step-up" readOnly value="12345" inputMode="numeric" aria-invalid aria-describedby="board-step-up-error" />
            <FieldError id="board-step-up-error">That code didn't work</FieldError>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline">Cancel</Button>
            <Button disabled>Verify</Button>
          </div>
        </DialogFrame>
      </State>
      <State id="m11-console-files" heading="UI-5 Console: files">
        <ConsoleTabs value="files" />
        <PathBar path="/etc" />
        <FilesTable entries={fx.filesDefault.entries} />
        <Frames>
          <Frame label="UX-9 row menu, protected path">
            <MenuFrame
              items={[
                { label: "Download" },
                { label: "Edit", reason: "Darkpanel's own files are read-only here" },
                { label: "Rename", reason: "Darkpanel's own files are read-only here" },
                { label: "Permissions", reason: "Darkpanel's own files are read-only here" },
                { label: "Delete", reason: "Darkpanel's own files are read-only here" },
              ]}
            />
          </Frame>
          <Frame label="UX-9 worst case: 2000 entries, 255-byte and escaped names">
            <FilesTable entries={fx.filesWorst.entries.slice(0, 9).filter((_, i) => i === 1 || i === 7 || i === 8)} total={fx.filesWorst.entries.length} />
          </Frame>
        </Frames>
      </State>
      <State id="m11-console-editor" heading="UI-5 Console: editor">
        <div className="flex flex-col gap-2">
          <Label htmlFor="ed-hosts">/etc/hosts</Label>
          <Textarea id="ed-hosts" readOnly rows={6} className="font-mono text-xs" value={"127.0.0.1 localhost\n127.0.1.1 web-1\n10.0.0.12 db-1\n::1 localhost ip6-localhost"} />
          <div className="flex gap-2">
            <Button>Save</Button>
            <Button variant="outline">Close</Button>
          </div>
        </div>
        <Alert role="status">
          <AlertDescription>This file is not text or is over 1 MiB. Download it instead.</AlertDescription>
        </Alert>
      </State>
      <State id="m11-console-conflict" heading="UI-5 Console: conflict">
        <Alert variant="warning" role="alert">
          <AlertTitle>Changed on the host since you opened it</AlertTitle>
          <AlertDescription className="flex flex-wrap gap-2">
            <Button size="sm" variant="outline">
              Reload
            </Button>
            <Button size="sm" variant="destructive">
              Overwrite
            </Button>
          </AlertDescription>
        </Alert>
      </State>
      <State id="m11-console-upload" heading="UI-5 Console: upload progress">
        <div className="flex max-w-lg flex-col gap-2">
          <span className="text-sm">Uploading backup-2026-11-26.tar.gz to /srv/uploads</span>
          <Progress value={62} aria-label="Upload progress" getValueLabel={(v) => `${v}%`} />
          <div className="flex items-center gap-3">
            <span className="text-xs text-muted-foreground">317 of 512 MiB · 62%</span>
            <Button size="sm" variant="outline">
              Cancel
            </Button>
          </div>
        </div>
      </State>
    </>
  )
}

// ---- UI-7 Jobs

const RESULT: Record<string, [Status, string]> = {
  success: ["ok", "Succeeded"],
  "exit-code": ["error", "Failed"],
  timeout: ["error", "Timed out"],
  "": ["info", "Running"],
}

function JobsTable({ jobs, total }: { jobs: fx.M11Job[]; total?: number }) {
  return (
    <>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Schedule</TableHead>
            <TableHead>User</TableHead>
            <TableHead>Last result</TableHead>
            <TableHead>Next run</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {jobs.map((j) => {
            const [status, label] = RESULT[j.lastResult] ?? ["off", j.lastResult]
            return (
              <TableRow key={j.slug}>
                <TableCell>
                  <Truncated text={j.slug} />
                  <span className="block text-xs text-muted-foreground">{j.description}</span>
                </TableCell>
                <TableCell>
                  {j.schedule === "hourly" ? "Every hour" : j.schedule.startsWith("Mon") ? "Mondays at 07:30" : "Every day at 03:00"}
                  <span className="block font-mono text-xs text-muted-foreground">{j.schedule}</span>
                </TableCell>
                <TableCell>{j.user}</TableCell>
                <TableCell>
                  <StatusPill status={status} label={j.exitStatus !== null && j.exitStatus !== 0 ? `${label} (exit ${j.exitStatus})` : label} />
                </TableCell>
                <TableCell>{j.enabled ? j.nextRun.slice(5, 16).replace("T", " ") : "Disabled"}</TableCell>
                <TableCell>
                  <RowMenu name={j.slug} />
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      {total !== undefined && <Count shown={jobs.length} total={total} thing="jobs" />}
    </>
  )
}

function JobsBoard() {
  return (
    <>
      <State id="m11-jobs-off" heading="UI-7 Jobs: off">
        <EmptyState title="Off on this host" description="Turning it on lets admins schedule commands to run on this host." action={<Button>Enable</Button>} />
      </State>
      <State id="m11-jobs-empty" heading="UI-7 Jobs: empty">
        <EmptyState title="No Darkpanel jobs yet" description="A job runs a command on a schedule as the user you pick." action={<Button>Add job</Button>} />
      </State>
      <State id="m11-jobs-default" heading="UI-7 Jobs: default">
        <div className="flex justify-end">
          <Button>Add job</Button>
        </div>
        <JobsTable jobs={fx.jobsDefault.jobs} />
        <h3 className="text-sm font-semibold">Other scheduled tasks</h3>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Task</TableHead>
              <TableHead>Schedule</TableHead>
              <TableHead>Source</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {fx.jobsDefault.timers.map((t) => (
              <TableRow key={t.name}>
                <TableCell className="font-mono text-xs">{t.name}</TableCell>
                <TableCell>{`Next ${t.nextRun.slice(5, 16).replace("T", " ")}`}</TableCell>
                <TableCell className="font-mono text-xs">{t.source}</TableCell>
              </TableRow>
            ))}
            {fx.jobsDefault.cron.map((c) => (
              <TableRow key={c.source}>
                <TableCell className="font-mono text-xs">
                  <Truncated text={c.command} />
                </TableCell>
                <TableCell className="font-mono text-xs">{c.schedule}</TableCell>
                <TableCell className="font-mono text-xs">{c.source}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </State>
      <State id="m11-jobs-form" heading="UI-7 Jobs: form with invalid schedule">
        <DialogFrame title="Add a job on web-1">
          <div className="flex flex-col gap-2">
            <Label htmlFor="j-name">Name</Label>
            <Input id="j-name" readOnly value="db-backup" />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="j-sched">Schedule</Label>
            <Input id="j-sched" readOnly value="*-*-* 25:00:00" className="font-mono" aria-invalid aria-describedby="j-sched-error" />
            <FieldError id="j-sched-error">systemd cannot read this schedule: hour 25 is out of range</FieldError>
            <p className="text-xs text-muted-foreground">A valid schedule shows its next three runs here.</p>
          </div>
          <div className="flex flex-col gap-2">
            <span className="text-sm font-medium">User</span>
            <Button variant="outline" className="justify-start">
              backup
            </Button>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="j-cmd">Command</Label>
            <Textarea id="j-cmd" readOnly rows={2} className="font-mono text-xs" value="/usr/local/bin/pg-backup --all" />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="flex flex-col gap-1">
              <Label htmlFor="j-timeout">Timeout (seconds)</Label>
              <Input id="j-timeout" readOnly value="3600" />
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="j-delay">Random delay (seconds)</Label>
              <Input id="j-delay" readOnly value="300" />
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="j-catch" checked />
            <Label htmlFor="j-catch">Catch up a run missed while the host was off</Label>
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="outline">Cancel</Button>
            <Button disabled>Review change</Button>
          </div>
        </DialogFrame>
      </State>
      <State id="m11-jobs-worst" heading="UI-7 Jobs: worst case">
        <JobsTable jobs={fx.jobsWorst.jobs.slice(0, SAMPLE)} total={fx.jobsWorst.jobs.length} />
      </State>
    </>
  )
}

// ---- UI-8 Tuning

const MIB = 1024 * 1024

function SwapCard({ state }: { state: typeof fx.swapDefault }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>Swap</CardTitle>
        <CardDescription>{`Memory ${Math.round(state.memoryTotal / MIB / 1024)} GiB, ${(state.memoryAvailable / MIB / 1024).toFixed(1)} GiB available`}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {state.swaps.map((s) => (
          <div key={s.path} className="flex flex-col gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-mono text-sm">{s.path}</span>
              {s.darkpanel && <Badge variant="outline">Darkpanel</Badge>}
              <span className="text-sm text-muted-foreground">{`${Math.round(s.used / MIB)} of ${Math.round(s.size / MIB)} MiB used`}</span>
            </div>
            <Progress value={s.used} max={s.size} aria-label={`${s.path} used`} getValueLabel={(v, m) => `${Math.round((v / m) * 100)}%`} />
            {s.darkpanel && (
              <div className="flex gap-2">
                <Button size="sm" variant="outline">
                  Resize
                </Button>
                <Button size="sm" variant="outline">
                  Remove
                </Button>
              </div>
            )}
          </div>
        ))}
        {state.unavailable !== "" ? (
          <Alert role="status">
            <AlertDescription>{state.unavailable}</AlertDescription>
          </Alert>
        ) : (
          !state.swaps.some((s) => s.darkpanel) && (
            <div>
              <Button>Create swap file</Button>
            </div>
          )
        )}
      </CardContent>
    </Card>
  )
}

type SysctlKey = (typeof fx.sysctlDefault.keys)[number]

function SysctlTable({ keys, invalid }: { keys: SysctlKey[]; invalid?: string }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Key</TableHead>
          <TableHead>Live</TableHead>
          <TableHead>Value at boot</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {keys.map((k) => {
          const id = `sys-${k.key}`
          const bad = invalid === k.key
          return (
            <TableRow key={k.key}>
              <TableCell>
                <span className="font-mono text-xs">{k.key}</span>
                {k.help !== "" && <span className="block text-xs text-muted-foreground">{k.help}</span>}
              </TableCell>
              <TableCell className="font-mono text-xs">{k.unavailable !== "" ? k.unavailable : k.live}</TableCell>
              <TableCell className="flex flex-col items-start gap-1">
                {k.owner !== "" ? (
                  <Badge variant="outline">{`Managed by ${k.owner}`}</Badge>
                ) : k.unavailable !== "" ? null : (
                  <>
                    <Input
                      id={id}
                      aria-label={k.key}
                      readOnly
                      value={bad ? "250" : k.set}
                      placeholder={k.live}
                      aria-invalid={bad}
                      aria-describedby={bad ? `${id}-error` : undefined}
                      className="max-w-40 font-mono"
                    />
                    {bad && <FieldError id={`${id}-error`}>{`Use a whole number from ${k.min} to ${k.max}`}</FieldError>}
                    {k.laterFiles.length > 0 && <span className="text-xs text-warning-text">{`${k.laterFiles[0]} sets it later at boot and wins`}</span>}
                  </>
                )}
              </TableCell>
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

function TuningBoard() {
  const keys = fx.sysctlDefault.keys
  return (
    <>
      <State id="m11-tuning-default" heading="UI-8 Tuning: default">
        <SwapCard state={fx.swapDefault} />
        <SysctlTable keys={keys} invalid="vm.swappiness" />
        <div>
          <Button>Review changes</Button>
        </div>
      </State>
      <State id="m11-tuning-locked" heading="UI-8 Tuning: locked key">
        <SysctlTable keys={keys.filter((k) => k.owner !== "")} />
      </State>
      <State id="m11-tuning-override" heading="UI-8 Tuning: override warning">
        <SysctlTable keys={keys.filter((k) => k.laterFiles.length > 0)} />
      </State>
      <State id="m11-tuning-unsupported" heading="UI-8 Tuning: unsupported filesystem">
        <SwapCard state={fx.swapUnsupported as typeof fx.swapDefault} />
      </State>
    </>
  )
}

const BOARDS: Record<M11BoardId, () => ReactNode> = {
  services: ServicesBoard,
  journal: JournalBoard,
  users: UsersBoard,
  fail2ban: Fail2banBoard,
  console: ConsoleBoard,
  jobs: JobsBoard,
  tuning: TuningBoard,
}

export function M11Boards() {
  const [params] = useSearchParams()
  const only = params.get("board") as M11BoardId | null
  // The kit pins html and body to the viewport for its app shell; a board
  // taller than the viewport would be screenshotted blank below the fold.
  useLayoutEffect(() => {
    const els = [document.documentElement, document.body]
    const saved = els.map((el) => [el.style.height, el.style.overflow] as const)
    for (const el of els) {
      el.style.height = "auto"
      el.style.overflow = "visible"
    }
    return () =>
      els.forEach((el, i) => {
        el.style.height = saved[i]![0]
        el.style.overflow = saved[i]![1]
      })
  }, [])
  const ids = only && only in BOARDS ? [only] : (Object.keys(BOARDS) as M11BoardId[])
  return (
    <main className="min-h-screen bg-background p-6 text-foreground">
      <h1 className="sr-only">M11 boards</h1>
      {ids.map((id) => {
        const Board = BOARDS[id]
        return (
          <div key={id} data-testid={`m11-board-${id}`} className="mx-auto mb-6 flex max-w-6xl flex-col gap-6 bg-background">
            <Board />
          </div>
        )
      })}
    </main>
  )
}
