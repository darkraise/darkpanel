import { useLayoutEffect, type ReactNode } from "react"
import { useSearchParams } from "react-router"
import { Area, AreaChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts"
import {
  AlertTriangle,
  Bell,
  CircleCheck,
  CircleX,
  Copy,
  Mail,
  MessageCircle,
  MoreHorizontal,
  OctagonAlert,
  Search,
  Send,
  Webhook,
} from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { ChartContainer, type ChartConfig } from "darkraise-ui/components/chart"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { SegmentGroup, SegmentGroupItem } from "darkraise-ui/components/segment-group"
import { Switch } from "darkraise-ui/components/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"

// The M10 boards (spec UI-1 to UI-7): every page and state the wave adds,
// drawn from kit components with local fixture data, so each can be judged
// against the UI/UX contract before the pages are built. Dialogs are drawn
// open in place, as frames, so several show at once.

export type M10BoardId = "users" | "groups" | "sign-in" | "notifications" | "alerts" | "metrics" | "login"

const LONG_GROUP = "production-eu-west-payments-and-billing-clusters" // 48 characters
const NOW = "Nov 24, 09:40"

// Tag marks the UX or OP items a frame illustrates, beside its UI heading.
function Tag({ ids }: { ids: string }) {
  return <span className="font-mono text-xs text-muted-foreground">{ids}</span>
}

// Frames lays out small labelled frames, such as a page's UX-6 states.
function Frames({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4">{children}</div>
}

function Frame({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2 rounded-md border border-dashed border-border p-3">
      <Tag ids={label} />
      {children}
    </div>
  )
}

// PageStates draws a page's loading, empty, error and admin-only states (UX-6).
function PageStates({ thing, empty, create }: { thing: string; empty: string; create: string }) {
  return (
    <Frames>
      <Frame label="UX-6 loading">
        <div className="flex flex-col gap-2" aria-label={`Loading ${thing}`}>
          <Skeleton className="h-6 w-full" />
          <Skeleton className="h-6 w-full" />
          <Skeleton className="h-6 w-2/3" />
        </div>
      </Frame>
      <Frame label="UX-6 empty">
        <p className="text-sm">{empty}</p>
        <div>
          <Button size="sm">{create}</Button>
        </div>
      </Frame>
      <Frame label="UX-6 error">
        <p className="text-sm">Couldn't load {thing}</p>
        <div>
          <Button size="sm" variant="outline">
            Retry
          </Button>
        </div>
      </Frame>
      <Frame label="UX-6 permission denied">
        <p className="text-sm">Only admins can see this page.</p>
      </Frame>
    </Frames>
  )
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

function PageHead({ title, action }: { title: string; action?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2">
      <h3 className="text-lg font-semibold">{title}</h3>
      {action}
    </div>
  )
}

// DialogFrame draws a dialog's content open in place.
function DialogFrame({ title, description, children, footer, ids }: { title: string; description?: string; children: ReactNode; footer: ReactNode; ids?: string }) {
  return (
    <div role="group" aria-label={`${title} dialog`} className="mx-auto flex w-full max-w-lg flex-col gap-4 rounded-lg border border-border bg-popover p-6 text-popover-foreground shadow-lg">
      {ids && <Tag ids={ids} />}
      <div className="flex flex-col gap-1">
        <h4 className="text-base font-semibold">{title}</h4>
        {description && <p className="text-sm text-muted-foreground">{description}</p>}
      </div>
      {children}
      <div className="flex flex-wrap justify-end gap-2">{footer}</div>
    </div>
  )
}

function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

// UI-1 — Users.

interface UserRow {
  username: string
  role: "Admin" | "Member"
  access: string
  methods: string
  state: string
}

const ME: UserRow = { username: "admin", role: "Admin", access: "All hosts", methods: "Password and code", state: "Active" }

const USERS: UserRow[] = [
  ME,
  { username: "mia", role: "Member", access: "Operator on web, db", methods: "Password and code, OIDC", state: "Active" },
  { username: "sam", role: "Member", access: "Viewer on all hosts", methods: "Tailscale", state: "Active" },
  { username: "noor", role: "Member", access: "Viewer on staging", methods: "None yet", state: "Invited, expires Nov 26" },
  { username: "ola", role: "Member", access: "Operator on db", methods: "Password and code", state: "Disabled" },
]

const WORST_USERS: UserRow[] = [
  ME,
  ...Array.from({ length: 49 }, (_, i): UserRow => ({
    username: i === 0 ? `operator-${"x".repeat(23)}` : `member-${String(i + 1).padStart(2, "0")}`,
    role: "Member",
    access: i === 0 ? `Operator on ${LONG_GROUP}, web, +30 more` : i % 2 ? "Viewer on all hosts" : "Operator on web, db",
    methods: i % 3 === 0 ? "Password and code, OIDC, Tailscale" : "Password and code",
    state: i % 7 === 0 ? "Invited, expires Nov 26" : "Active",
  })),
]

// An invited user's row menu; an active user's has Disable in place of the
// invite entries, a disabled user's Enable.
const ROW_MENU = ["Change role", "Edit access", "Edit sign-in links", "New enrollment link", "Cancel invite"]

function UsersTable({ rows }: { rows: UserRow[] }) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Username</TableHead>
          <TableHead>Role</TableHead>
          <TableHead>Access</TableHead>
          <TableHead>Sign-in methods</TableHead>
          <TableHead>State</TableHead>
          <TableHead className="w-10">
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((u) => (
          <TableRow key={u.username}>
            <TableCell className="max-w-48 truncate font-medium" title={u.username}>
              {u.username}
            </TableCell>
            <TableCell>
              <Badge variant={u.role === "Admin" ? "default" : "secondary"}>{u.role}</Badge>
            </TableCell>
            <TableCell className="max-w-72 truncate" title={u.access}>
              {u.access}
            </TableCell>
            <TableCell>{u.methods}</TableCell>
            <TableCell>{u.state}</TableCell>
            <TableCell>
              <Button variant="ghost" size="icon" aria-label={`Actions for ${u.username}`}>
                <MoreHorizontal aria-hidden />
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function UsersPage({ rows, menu }: { rows: UserRow[]; menu?: boolean }) {
  return (
    <div className="flex flex-col gap-4">
      <PageHead title="Users" action={<Button>Invite user</Button>} />
      <UsersTable rows={rows} />
      {menu && (
        <Frame label="UI-1 row menu of noor, invited">
          <div role="menu" aria-label="Actions for noor" className="flex w-56 flex-col rounded-md border border-border bg-popover p-1 text-sm shadow-md">
            {ROW_MENU.map((item) => (
              <div key={item} role="menuitem" className="rounded-sm px-2 py-1.5">
                {item}
              </div>
            ))}
          </div>
        </Frame>
      )}
    </div>
  )
}

function InviteDialog() {
  const groups = [
    { name: "web", role: "Operator", on: true },
    { name: "db", role: "Viewer", on: true },
    { name: "staging", role: "Viewer", on: false },
  ]
  return (
    <DialogFrame
      ids="UX-1"
      title="Invite user"
      description="The user sets a password and a code from a link you send them."
      footer={
        <>
          <Button variant="outline">Cancel</Button>
          <Button>Review invite</Button>
        </>
      }
    >
      <Field id="invite-username" label="Username">
        <Input id="invite-username" defaultValue="noor" />
      </Field>
      <div className="flex flex-col gap-1.5">
        <span className="text-sm font-medium">Role</span>
        <SegmentGroup defaultValue="member" aria-label="Role" variant="outline">
          <SegmentGroupItem value="member">Member</SegmentGroupItem>
          <SegmentGroupItem value="admin">Admin</SegmentGroupItem>
        </SegmentGroup>
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-1 text-sm font-medium">Access</legend>
        <div className="flex items-center justify-between gap-2 text-sm">
          <label className="flex items-center gap-2">
            <Checkbox aria-label="All hosts" /> All hosts
          </label>
          <SegmentGroup defaultValue="Viewer" aria-label="Role on all hosts" variant="outline" disabled>
            <SegmentGroupItem value="Viewer">Viewer</SegmentGroupItem>
            <SegmentGroupItem value="Operator">Operator</SegmentGroupItem>
          </SegmentGroup>
        </div>
        {groups.map((g) => (
          <div key={g.name} className="flex items-center justify-between gap-2 text-sm">
            <label className="flex items-center gap-2">
              <Checkbox defaultChecked={g.on} aria-label={g.name} /> {g.name}
            </label>
            <SegmentGroup defaultValue={g.role} aria-label={`Role on ${g.name}`} variant="outline" disabled={!g.on}>
              <SegmentGroupItem value="Viewer">Viewer</SegmentGroupItem>
              <SegmentGroupItem value="Operator">Operator</SegmentGroupItem>
            </SegmentGroup>
          </div>
        ))}
      </fieldset>
      <Field id="invite-email" label="Email (optional)" hint="Links an OIDC account with this address.">
        <Input id="invite-email" type="email" placeholder="noor@example.com" />
      </Field>
      <Field id="invite-tailnet" label="Tailnet login (optional)" hint="Links a Tailscale identity.">
        <Input id="invite-tailnet" placeholder="noor@github" />
      </Field>
    </DialogFrame>
  )
}

function LinkOnceDialog() {
  return (
    <DialogFrame ids="UX-1" title="Enrollment link for noor" description="Copy this link now. It is shown once." footer={<Button>Done</Button>}>
      <code aria-label="Enrollment link" className="rounded-md border border-border bg-muted p-2 font-mono text-sm break-all">
        https://panel.example.com/enroll#q3X9-7hPz-Lm2c-Vt8R-w4Kd-9sQe
      </code>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">Expires Nov 26, 09:40</p>
        <Button variant="outline">
          <Copy aria-hidden /> Copy
        </Button>
      </div>
    </DialogFrame>
  )
}

function UsersBoard() {
  return (
    <>
      <State id="m10-users-default" heading="UI-1 Users: default">
        <UsersPage rows={USERS} menu />
      </State>
      <State id="m10-users-empty" heading="UI-1 Users: empty but me">
        <UsersPage rows={[ME]} />
      </State>
      <State id="m10-users-worst" heading="UI-1 Users: worst case">
        <UsersPage rows={WORST_USERS} />
      </State>
      <State id="m10-users-invite" heading="UI-1 Users: invite dialog">
        <InviteDialog />
      </State>
      <State id="m10-users-link" heading="UI-1 Users: link shown once">
        <LinkOnceDialog />
      </State>
    </>
  )
}

// UI-2 — Host groups.

const GROUPS = [
  { name: "web", hosts: 4, grants: 2 },
  { name: "db", hosts: 2, grants: 1 },
  { name: "staging", hosts: 6, grants: 0 },
  { name: LONG_GROUP, hosts: 200, grants: 12 },
  ...Array.from({ length: 28 }, (_, i) => ({ name: `team-${String(i + 1).padStart(2, "0")}`, hosts: (i % 9) + 1, grants: i % 3 })),
]

const HOSTS = Array.from({ length: 200 }, (_, i) => `web-${String(i + 1).padStart(3, "0")}`)

function GroupsBoard() {
  return (
    <State id="m10-groups" heading="UI-2 Host groups">
      <div className="flex flex-col gap-4">
        <PageHead title="Host groups" action={<Button>Create group</Button>} />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead className="text-right">Hosts</TableHead>
              <TableHead className="text-right">Grants</TableHead>
              <TableHead className="w-10">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {GROUPS.map((g) => (
              <TableRow key={g.name}>
                <TableCell className="max-w-96 truncate font-medium" title={g.name}>
                  {g.name}
                </TableCell>
                <TableCell className="text-right tabular-nums">{g.hosts}</TableCell>
                <TableCell className="text-right tabular-nums">{g.grants}</TableCell>
                <TableCell>
                  <Button variant="ghost" size="icon" aria-label={`Actions for ${g.name}`}>
                    <MoreHorizontal aria-hidden />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <DialogFrame
          title="Members of web"
          description="4 of 200 hosts are members."
          footer={
            <>
              <Button variant="outline">Cancel</Button>
              <Button>Review members</Button>
            </>
          }
        >
          <div className="relative">
            <Search aria-hidden className="absolute top-1/2 left-2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input aria-label="Search hosts" placeholder="Search hosts" className="pl-8" />
          </div>
          <ul className="flex max-h-56 flex-col gap-2 overflow-y-auto text-sm" aria-label="Hosts">
            {HOSTS.map((h, i) => (
              <li key={h}>
                <label className="flex items-center gap-2">
                  <Checkbox defaultChecked={i < 4} aria-label={h} /> {h}
                </label>
              </li>
            ))}
          </ul>
        </DialogFrame>
        <PageStates thing="host groups" empty="No host groups yet. Group hosts to grant access to them together." create="Create group" />
      </div>
    </State>
  )
}

// UI-3 — Sign-in settings.

function SignInBoard() {
  return (
    <State id="m10-sign-in" heading="UI-3 Sign-in">
      <div className="flex flex-col gap-4">
        <PageHead title="Sign-in" />
        <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
          <Card>
            <CardHeader className="flex flex-row items-start justify-between gap-2 space-y-0">
              <div className="flex flex-col gap-1">
                <CardTitle>OIDC</CardTitle>
                <CardDescription>Sign in through your identity provider.</CardDescription>
              </div>
              <Badge variant="success">Configured</Badge>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <Field id="oidc-issuer" label="Issuer URL">
                <Input id="oidc-issuer" defaultValue="https://login.example.com/realms/ops" />
              </Field>
              <Field id="oidc-client" label="Client ID">
                <Input id="oidc-client" defaultValue="darkpanel" />
              </Field>
              <Field id="oidc-secret" label="Client secret" hint="Saved — enter a new value to replace it.">
                <Input id="oidc-secret" type="password" placeholder="••••••••" />
              </Field>
              <Field id="oidc-name" label="Button name" hint='Shown on the login page as "Sign in with Acme SSO".'>
                <Input id="oidc-name" defaultValue="Acme SSO" />
              </Field>
              <Field id="oidc-redirect" label="Redirect base" hint="The address users open Darkpanel at.">
                <Input id="oidc-redirect" defaultValue="https://panel.example.com" />
              </Field>
              <label className="flex items-center justify-between gap-2 text-sm">
                Require Darkpanel code too <Switch defaultChecked aria-label="Require Darkpanel code too" />
              </label>
              <div className="flex flex-wrap gap-2">
                <Button>Save</Button>
                <Button variant="outline">Turn off</Button>
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="flex flex-row items-start justify-between gap-2 space-y-0">
              <div className="flex flex-col gap-1">
                <CardTitle>Tailscale identity</CardTitle>
                <CardDescription>Sign in as the tailnet user of the device you browse from.</CardDescription>
              </div>
              <Badge variant="secondary">Off</Badge>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <label className="flex items-center justify-between gap-2 text-sm">
                Sign in with Tailscale identity <Switch aria-label="Sign in with Tailscale identity" />
              </label>
              <label className="flex items-center justify-between gap-2 text-sm">
                Require Darkpanel code too <Switch aria-label="Require Darkpanel code too for Tailscale" />
              </label>
              <p className="text-sm text-muted-foreground">Works only on the tailnet address, panel.tail1234.ts.net.</p>
              <div>
                <Button>Save</Button>
              </div>
            </CardContent>
          </Card>
          <Card>
            <CardHeader className="flex flex-row items-start justify-between gap-2 space-y-0">
              <div className="flex flex-col gap-1">
                <CardTitle>OIDC</CardTitle>
                <CardDescription>Sign in through your identity provider.</CardDescription>
              </div>
              <Badge variant="secondary">Off</Badge>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <Field id="oidc-off-issuer" label="Issuer URL">
                <Input id="oidc-off-issuer" placeholder="https://login.example.com" />
              </Field>
              <Field id="oidc-off-client" label="Client ID">
                <Input id="oidc-off-client" />
              </Field>
              <Field id="oidc-off-secret" label="Client secret">
                <Input id="oidc-off-secret" type="password" />
              </Field>
              <div>
                <Button>Save</Button>
              </div>
            </CardContent>
          </Card>
        </div>
      </div>
    </State>
  )
}

// UI-4 — Notifications.

const KINDS = [
  { kind: "ntfy", icon: Bell },
  { kind: "Telegram", icon: Send },
  { kind: "Discord", icon: MessageCircle },
  { kind: "Email", icon: Mail },
  { kind: "Webhook", icon: Webhook },
]

const CHANNELS = [
  { kind: 0, name: "ops-phone", routing: "Warning and above, all events, all hosts", last: "Delivered Nov 24, 09:12", state: "ok" },
  { kind: 1, name: "team-chat", routing: "Critical, alerts only, group web", last: "Failed: 403", state: "failed" },
  { kind: 2, name: "incidents", routing: "Warning and above, all events, groups web, db", last: "Sending test…", state: "pending" },
  { kind: 3, name: "oncall", routing: "Critical, all events, all hosts", last: "No deliveries yet", state: "none" },
  { kind: 4, name: "audit-sink", routing: "Info and above, change events, all hosts", last: "Delivered Nov 24, 08:55", state: "ok" },
]

function LastDelivery({ text, state }: { text: string; state: string }) {
  const Icon = state === "ok" ? CircleCheck : state === "failed" ? CircleX : null
  return (
    <p className="flex items-center gap-1.5 text-muted-foreground">
      {Icon && <Icon aria-hidden className="size-4" />}
      <span className={state === "failed" ? "font-medium text-foreground" : undefined}>Last delivery: {text}</span>
    </p>
  )
}

function NotificationsBoard() {
  return (
    <State id="m10-notifications" heading="UI-4 Notifications">
      <div className="flex flex-col gap-4">
        <PageHead title="Notifications" action={<Button>Add channel</Button>} />
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          {CHANNELS.map((c) => {
            const { kind, icon: Icon } = KINDS[c.kind]!
            return (
              <Card key={c.name}>
                <CardHeader className="flex flex-row items-center gap-3 space-y-0">
                  <Icon aria-hidden className="size-5 text-muted-foreground" />
                  <div className="flex min-w-0 flex-col">
                    <CardTitle className="truncate">{c.name}</CardTitle>
                    <CardDescription>{kind}</CardDescription>
                  </div>
                </CardHeader>
                <CardContent className="flex flex-col gap-3 text-sm">
                  <p>{c.routing}</p>
                  <LastDelivery text={c.last} state={c.state} />
                  <div>
                    <Button variant="outline" size="sm" disabled={c.state === "pending"}>
                      {c.state === "pending" ? "Sending test…" : "Send test"}
                    </Button>
                  </div>
                </CardContent>
              </Card>
            )
          })}
        </div>
        <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
          <DialogFrame ids="UX-4 step 1" title="Add channel" description="Choose where notifications go." footer={<Button variant="outline">Cancel</Button>}>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
              {KINDS.map(({ kind, icon: Icon }) => (
                <Button key={kind} variant="outline" className="justify-start">
                  <Icon aria-hidden /> {kind}
                </Button>
              ))}
            </div>
          </DialogFrame>
          <DialogFrame
            ids="UX-4 step 2, inline validation"
            title="Add an email channel"
            footer={
              <>
                <Button variant="outline">Back</Button>
                <Button>Review channel</Button>
              </>
            }
          >
            <Field id="mail-name" label="Name">
              <Input id="mail-name" defaultValue="oncall" />
            </Field>
            <Field id="mail-host" label="SMTP host">
              <Input id="mail-host" defaultValue="smtp.example.com" />
            </Field>
            <Field id="mail-port" label="Port" hint="Port must be 1 to 65535.">
              <Input id="mail-port" defaultValue="70000" aria-invalid />
            </Field>
            <Field id="mail-password" label="Password" hint="Write-only: never shown again.">
              <Input id="mail-password" type="password" />
            </Field>
            <Field id="mail-to" label="Recipients" hint="Enter 1 to 10 addresses.">
              <Input id="mail-to" defaultValue="a@example.com, b@example.com, … (11 addresses)" aria-invalid />
            </Field>
            <Field id="ntfy-url" label="ntfy server URL (the ntfy form)" hint="Enter a URL that starts with https://.">
              <Input id="ntfy-url" defaultValue="ntfy.example.com" aria-invalid />
            </Field>
          </DialogFrame>
        </div>
        <PageStates thing="notification channels" empty="No channels yet. Add one to get notified of alerts and failed changes." create="Add channel" />
      </div>
    </State>
  )
}

// UI-5 — Alerts.

const FIRING = [
  { severity: "Critical", host: "db-1", subject: "Host offline", value: "No connection", since: "Nov 24, 09:28" },
  { severity: "Warning", host: "web-2", subject: "Disk /", value: "93.4 %", since: "Nov 24, 08:51" },
  { severity: "Warning", host: "web-3", subject: "Disk /var/lib/docker", value: "91.2 %", since: "Nov 24, 07:05" },
  ...Array.from({ length: 17 }, (_, i) => ({
    severity: i % 4 === 0 ? "Critical" : "Warning",
    host: `web-${String(i + 4).padStart(3, "0")}`,
    subject: i % 4 === 0 ? "Load per CPU" : `Disk /srv/data-${i}`,
    value: i % 4 === 0 ? "2.4 per CPU" : `${90 + (i % 9)}.${i % 10} %`,
    since: `Nov 24, 0${i % 9}:${String((i * 7) % 60).padStart(2, "0")}`,
  })),
]

const RULES = [
  { name: "Host offline", condition: "Offline", duration: "5 minutes", severity: "Critical", target: "All hosts", on: true },
  { name: "Disk almost full", condition: "Disk above 90 %", duration: "10 minutes", severity: "Warning", target: "All hosts", on: true },
  { name: "Busy CPU", condition: "CPU above 85 %", duration: "15 minutes", severity: "Warning", target: "Group web", on: false },
  { name: "Load per CPU", condition: "Load above 2 per CPU", duration: "10 minutes", severity: "Critical", target: `Group ${LONG_GROUP}`, on: true },
]

function Severity({ level }: { level: string }) {
  const Icon = level === "Critical" ? OctagonAlert : AlertTriangle
  return (
    <Badge variant={level === "Critical" ? "destructive" : "warning"}>
      <Icon aria-hidden className="size-3.5" />
      {level}
    </Badge>
  )
}

function AlertsBoard() {
  return (
    <State id="m10-alerts" heading="UI-5 Alerts">
      <div className="flex flex-col gap-4">
        <PageHead title="Alerts" action={<Button>New rule</Button>} />
        <h4 className="text-base font-semibold">Firing</h4>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Severity</TableHead>
              <TableHead>Host</TableHead>
              <TableHead>Alert</TableHead>
              <TableHead className="text-right">Value</TableHead>
              <TableHead>Since</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {FIRING.map((a) => (
              <TableRow key={a.host + a.subject}>
                <TableCell>
                  <Severity level={a.severity} />
                </TableCell>
                <TableCell className="font-medium">{a.host}</TableCell>
                <TableCell>{a.subject}</TableCell>
                <TableCell className="text-right tabular-nums">{a.value}</TableCell>
                <TableCell>{a.since}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <h4 className="text-base font-semibold">Rules</h4>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Condition</TableHead>
              <TableHead>For</TableHead>
              <TableHead>Severity</TableHead>
              <TableHead>Hosts</TableHead>
              <TableHead>Enabled</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {RULES.map((r) => (
              <TableRow key={r.name}>
                <TableCell className="font-medium">{r.name}</TableCell>
                <TableCell>{r.condition}</TableCell>
                <TableCell>{r.duration}</TableCell>
                <TableCell>
                  <Severity level={r.severity} />
                </TableCell>
                <TableCell className="max-w-72 truncate" title={r.target}>
                  {r.target}
                </TableCell>
                <TableCell>
                  <Switch defaultChecked={r.on} aria-label={`${r.name} enabled`} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        <DialogFrame
          ids="UX-5"
          title="New rule"
          footer={
            <>
              <Button variant="outline">Cancel</Button>
              <Button>Review rule</Button>
            </>
          }
        >
          <Field id="rule-name" label="Name">
            <Input id="rule-name" defaultValue="Busy CPU" />
          </Field>
          <div className="flex flex-col gap-1.5">
            <span className="text-sm font-medium">Metric</span>
            <SegmentGroup defaultValue="cpu" aria-label="Metric" variant="outline">
              {["CPU", "Memory", "Swap", "Disk", "Load", "Offline"].map((m) => (
                <SegmentGroupItem key={m} value={m.toLowerCase()}>
                  {m}
                </SegmentGroupItem>
              ))}
            </SegmentGroup>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <Field id="rule-threshold" label="Above (%)">
              <Input id="rule-threshold" defaultValue="85" inputMode="decimal" />
            </Field>
            <Field id="rule-duration" label="For (minutes)" hint="1 to 1440.">
              <Input id="rule-duration" defaultValue="15" inputMode="numeric" />
            </Field>
          </div>
          <div className="flex flex-col gap-1.5">
            <span className="text-sm font-medium">Severity</span>
            <SegmentGroup defaultValue="warning" aria-label="Severity" variant="outline">
              <SegmentGroupItem value="warning">Warning</SegmentGroupItem>
              <SegmentGroupItem value="critical">Critical</SegmentGroupItem>
            </SegmentGroup>
          </div>
          <Field id="rule-target" label="Hosts" hint="All hosts, or one host group.">
            <Input id="rule-target" defaultValue="Group web" />
          </Field>
        </DialogFrame>
        <PageStates thing="alert rules" empty="No alert rules. Add one to hear about busy or full hosts." create="New rule" />
      </div>
    </State>
  )
}

// UI-6 — Host metrics.

// series is 60 minutes of a smooth fixture signal; minutes 38 to 41 are
// missing, so the gap is drawn as a break.
function series(base: number, swing: number, phase: number): (number | null)[] {
  return Array.from({ length: 60 }, (_, i) => (i >= 38 && i <= 41 ? null : Math.round((base + swing * Math.sin(i / 7 + phase)) * 100) / 100))
}

const MINUTES = Array.from({ length: 60 }, (_, i) => `09:${String(i).padStart(2, "0")}`)

function rows(cols: Record<string, (number | null)[]>) {
  return MINUTES.map((t, i) => ({ t, ...Object.fromEntries(Object.entries(cols).map(([k, v]) => [k, v[i]])) }))
}

function summary(values: (number | null)[], unit: string): string {
  const v = values.filter((x): x is number => x !== null)
  const f = (x: number) => `${x.toFixed(1)}${unit}`
  return `latest ${f(v[v.length - 1]!)}, min ${f(Math.min(...v))}, max ${f(Math.max(...v))}`
}

// A series after the first is dashed, so series differ by more than colour.
function dash(i: number): string | undefined {
  return i === 0 ? undefined : "6 3"
}

interface ChartSpec {
  title: string
  unit: string
  series: Record<string, { label: string; values: (number | null)[] }>
  area?: boolean
}

const CHARTS: ChartSpec[] = [
  { title: "CPU (%)", unit: " %", area: true, series: { cpu: { label: "CPU", values: series(32, 18, 0) } } },
  { title: "Memory (GB)", unit: " GB", area: true, series: { mem: { label: "Used", values: series(5.2, 0.6, 1) } } },
  {
    title: "Disk per mount (%)",
    unit: " %",
    series: { root: { label: "/", values: series(71, 2, 2) }, data: { label: "/data", values: series(48, 3, 3) } },
  },
  {
    title: "Network per interface (MB/s)",
    unit: " MB/s",
    series: { rx: { label: "eth0 in", values: series(4.1, 2.2, 4) }, tx: { label: "eth0 out", values: series(1.6, 0.9, 5) } },
  },
  { title: "Load per CPU", unit: "", series: { load: { label: "1-minute load", values: series(0.6, 0.3, 6) } } },
]

// colour is the kit token of a chart's i-th series. The kit defines
// --chart-N on the root; its --color-chart-N alias exists only where a
// utility class uses it, so series name the root token.
function colour(i: number): string {
  return `var(--chart-${i + 1})`
}

function MetricChart({ spec }: { spec: ChartSpec }) {
  const keys = Object.keys(spec.series)
  const config: ChartConfig = Object.fromEntries(keys.map((k, i) => [k, { label: spec.series[k]!.label, color: colour(i) }]))
  const data = rows(Object.fromEntries(keys.map((k) => [k, spec.series[k]!.values])))
  const text = keys.map((k) => `${spec.series[k]!.label}: ${summary(spec.series[k]!.values, spec.unit)}`).join("; ")
  const Chart = spec.area ? AreaChart : LineChart
  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{spec.title}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <ChartContainer config={config} description={`${spec.title}, last hour. ${text}`} aria-label={`${spec.title}, last hour. ${text}`} aria-describedby={undefined} tabIndex={0} className="h-40 w-full">
          <Chart data={data} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="t" tickLine={false} axisLine={false} ticks={["09:00", "09:15", "09:30", "09:45", "09:59"]} />
            <YAxis tickLine={false} axisLine={false} width={36} />
            {keys.map((k, i) =>
              spec.area ? (
                <Area key={k} dataKey={k} type="monotone" stroke={colour(i)} fill={colour(i)} fillOpacity={0.2} connectNulls={false} isAnimationActive={false} />
              ) : (
                <Line key={k} dataKey={k} type="monotone" stroke={colour(i)} strokeWidth={2} strokeDasharray={dash(i)} dot={false} connectNulls={false} isAnimationActive={false} />
              ),
            )}
          </Chart>
        </ChartContainer>
        <ul className="flex flex-col gap-0.5 text-xs text-muted-foreground" aria-hidden>
          {keys.map((k, i) => (
            <li key={k} className="flex items-center gap-2">
              <svg width="20" height="6" className="shrink-0">
                <line x1="0" y1="3" x2="20" y2="3" stroke={colour(i)} strokeWidth="2" strokeDasharray={dash(i)} />
              </svg>
              {spec.series[k]!.label}: {summary(spec.series[k]!.values, spec.unit)}
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  )
}

function MetricsBoard() {
  return (
    <State id="m10-metrics" heading="UI-6 Host metrics">
      <div className="flex flex-col gap-4">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-lg font-semibold">Metrics</h3>
          <SegmentGroup defaultValue="1h" aria-label="Range" variant="outline">
            {["1h", "24h", "7d", "30d", "90d"].map((r) => (
              <SegmentGroupItem key={r} value={r}>
                {r}
              </SegmentGroupItem>
            ))}
          </SegmentGroup>
        </div>
        <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
          {CHARTS.map((c) => (
            <MetricChart key={c.title} spec={c} />
          ))}
        </div>
        <h4 className="text-base font-semibold">Section states</h4>
        <Frames>
          <Frame label="UX-7 no data yet">
            <Alert>
              <AlertDescription>Collecting — the first samples appear within 2 minutes</AlertDescription>
            </Alert>
          </Frame>
          <Frame label="UX-7 host offline, charts keep history">
            <Alert variant="warning">
              <AlertDescription>Offline since {NOW}</AlertDescription>
            </Alert>
          </Frame>
          <Frame label="UX-7 module disabled">
            <Alert>
              <AlertDescription className="flex flex-wrap items-center gap-2">
                Metrics are off on this host <Button size="sm">Enable</Button>
              </AlertDescription>
            </Alert>
          </Frame>
          <Frame label="UX-7 polling failing">
            <Alert variant="destructive">
              <AlertDescription>No new samples since {NOW}: the host did not answer</AlertDescription>
            </Alert>
          </Frame>
        </Frames>
      </div>
    </State>
  )
}

// UI-7 — Login additions and enrollment, in the login page's layout
// (pages/login.tsx): the logo tile and the page title over the card.

function LoginFrame({ title, tag, children }: { title: string; tag: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <Tag ids={tag} />
      <Card className="w-full max-w-sm">
        <CardHeader className="flex flex-col items-center gap-3 text-center">
          <span role="img" aria-label="Darkpanel" className="flex size-12 items-center justify-center rounded-xl bg-primary text-xl font-semibold text-primary-foreground">
            <span aria-hidden>D</span>
          </span>
          <CardTitle>
            <span className="text-xl font-medium">{title}</span>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">{children}</CardContent>
      </Card>
    </div>
  )
}

function PasswordForm({ prefix }: { prefix: string }) {
  return (
    <>
      <Field id={`${prefix}-user`} label="Username">
        <Input id={`${prefix}-user`} />
      </Field>
      <Field id={`${prefix}-pass`} label="Password">
        <Input id={`${prefix}-pass`} type="password" />
      </Field>
      <Field id={`${prefix}-code`} label="Authentication code">
        <Input id={`${prefix}-code`} inputMode="numeric" />
      </Field>
      <Button>Log in</Button>
    </>
  )
}

function LoginBoard() {
  return (
    <State id="m10-login" heading="UI-7 Login">
      <div className="grid grid-cols-1 gap-6 md:grid-cols-2 xl:grid-cols-3">
        <LoginFrame title="Sign in" tag="UI-7 on the Tailscale listener, OIDC on">
          <Button variant="secondary">Continue as mia (mia@github, from laptop)</Button>
          <PasswordForm prefix="login" />
          <Button variant="outline">Sign in with Acme SSO</Button>
        </LoginFrame>
        <LoginFrame title="Sign in" tag="UX-3 code step first (requireTotp)">
          <p className="text-sm text-muted-foreground">Enter the code from your authenticator app to finish signing in as mia.</p>
          <Field id="totp-code" label="Authentication code">
            <Input id="totp-code" inputMode="numeric" />
          </Field>
          <Button>Log in</Button>
        </LoginFrame>
        <LoginFrame title="Sign in" tag="UX-3 no linked user, focused">
          <Alert variant="destructive" role="alert">
            <AlertDescription>No Darkpanel user is linked to this account. Ask an admin to add you.</AlertDescription>
          </Alert>
          <PasswordForm prefix="nolink" />
          <Button variant="outline">Sign in with Acme SSO</Button>
        </LoginFrame>
        <LoginFrame title="Set up your account" tag="UX-2 enrollment">
          <p className="text-sm text-muted-foreground">You are joining as noor. Choose a password, then add the code.</p>
          <Field id="enroll-pass" label="Password" hint="At least 12 characters.">
            <Input id="enroll-pass" type="password" />
          </Field>
          <Field id="enroll-confirm" label="Confirm password">
            <Input id="enroll-confirm" type="password" />
          </Field>
          <div className="flex size-32 items-center justify-center self-center rounded-md border border-border text-xs text-muted-foreground">
            QR code
          </div>
          <p className="text-sm">
            Or enter this key: <code className="font-mono break-all">JBSW Y3DP EHPK 3PXP</code>
          </p>
          <Field id="enroll-code" label="Authentication code">
            <Input id="enroll-code" inputMode="numeric" />
          </Field>
          <Button>Finish setup</Button>
        </LoginFrame>
        <LoginFrame title="Set up your account" tag="UX-2 expired or used link">
          <Alert variant="destructive">
            <AlertDescription>This link has expired. Ask an admin for a new one.</AlertDescription>
          </Alert>
        </LoginFrame>
      </div>
    </State>
  )
}

const BOARDS: Record<M10BoardId, () => ReactNode> = {
  users: UsersBoard,
  groups: GroupsBoard,
  "sign-in": SignInBoard,
  notifications: NotificationsBoard,
  alerts: AlertsBoard,
  metrics: MetricsBoard,
  login: LoginBoard,
}

// M10Boards renders every board, or the one ?board= names.
export function M10Boards() {
  const [params] = useSearchParams()
  const only = params.get("board") as M10BoardId | null
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
  const ids = only && only in BOARDS ? [only] : (Object.keys(BOARDS) as M10BoardId[])
  return (
    <main className="min-h-screen bg-background p-6 text-foreground">
      <h1 className="sr-only">M10 boards</h1>
      {ids.map((id) => {
        const Board = BOARDS[id]
        return (
          <div key={id} data-testid={`m10-board-${id}`} className="mx-auto mb-6 flex max-w-6xl flex-col gap-6 bg-background">
            <Board />
          </div>
        )
      })}
    </main>
  )
}
