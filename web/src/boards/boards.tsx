/* eslint-disable react-refresh/only-export-components -- an entry module like main.tsx: nothing imports it, so Fast Refresh has no boundary to keep */
import { StrictMode, type ReactNode } from "react"
import { createRoot } from "react-dom/client"
import { Link, MemoryRouter } from "react-router"
import { QueryClient } from "@tanstack/react-query"
import "@fontsource-variable/hanken-grotesk"
import "@fontsource-variable/jetbrains-mono"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge, type BadgeVariant } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { stateVariant } from "@/lib/badges"
import { formatTime } from "@/lib/format"
import { Providers } from "@/providers"
import "../styles/globals.css"

// The boards are static mock-ups of the screens' states, drawn before the
// screens are built; their data shapes are local so they never depend on the
// app's types.

interface BoardHost {
  id: string
  name: string
  isLocal: boolean
  state: "pending" | "enrolled" | "revoked"
  online: boolean
  lastSeenAt: string | null
  agentVersion: string | null
  protocolVersion: number | null
  os: string | null
  arch: string | null
  supported: boolean
  modules: { id: string; text: string; variant: BadgeVariant }[]
  ingestionError: string | null
}

interface BoardTransaction {
  startedAt: string
  kind: "update" | "rollback"
  fromVersion: string
  toVersion: string
  state: string
  error: string | null
}

interface BoardRollout {
  id: string
  version: string
  channel: "stable" | "beta"
  state: string
  error: string | null
  createdAt: string
  updatedAt: string
  hosts: { hostId: string; name: string | null; state: string; error: string | null }[]
}

const LONG_NAME = `web-${"x".repeat(59)}`
const LONG_VERSION = `1.0.0-beta.1+${"a".repeat(27)}`
const SEEN = "2026-11-11T08:00:00Z"

function host(over: Partial<BoardHost>): BoardHost {
  return {
    id: "h1",
    name: "web-1",
    isLocal: false,
    state: "enrolled",
    online: true,
    lastSeenAt: SEEN,
    agentVersion: "1.0.0",
    protocolVersion: 1,
    os: "debian-12",
    arch: "amd64",
    supported: true,
    modules: [
      { id: "firewall", text: "on", variant: "default" },
      { id: "docker", text: "off", variant: "outline" },
    ],
    ingestionError: null,
    ...over,
  }
}

const PENDING = host({
  id: "new-1",
  name: "new-1",
  state: "pending",
  online: false,
  lastSeenAt: null,
  agentVersion: null,
  protocolVersion: null,
  os: null,
  arch: null,
  modules: [],
})
const REVOKED = host({ id: "old-1", name: "old-1", state: "revoked", online: false, lastSeenAt: "2026-11-01T12:00:00Z" })
const OFFLINE = host({ id: "db-1", name: "db-1", online: false, lastSeenAt: "2026-11-10T22:15:00Z" })

const MIXED: BoardHost[] = [
  host({ id: "local", name: "controller", isLocal: true }),
  host({ id: "web-2", name: "web-2", ingestionError: "journal cursor lost" }),
  OFFLINE,
  host({ id: "edge-1", name: "edge-1", agentVersion: "0.9.0", supported: false }),
  PENDING,
  REVOKED,
]

const WORST: BoardHost[] = Array.from({ length: 50 }, (_, i) =>
  host({
    id: `w${i}`,
    name: i === 0 ? LONG_NAME : `web-${i + 1}`,
    agentVersion: i === 0 ? LONG_VERSION : "1.0.0",
    online: i % 7 !== 3,
  }),
)

const HISTORY: BoardTransaction[] = [
  { startedAt: "2026-11-11T07:00:00Z", kind: "update", fromVersion: "1.0.0", toVersion: "1.1.0", state: "committed", error: null },
  { startedAt: "2026-11-04T07:00:00Z", kind: "rollback", fromVersion: "1.0.1", toVersion: "1.0.0", state: "committed", error: null },
  {
    startedAt: "2026-11-04T06:58:00Z",
    kind: "update",
    fromVersion: "1.0.0",
    toVersion: "1.0.1",
    state: "failed",
    error: "the new agent did not report healthy within 60 seconds",
  },
]

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-4 rounded-lg border border-border p-4">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">{title}</h2>
      {children}
    </div>
  )
}

function HostsHeader() {
  return <PageHeader title="Hosts" description="The servers this controller manages." actions={<Button>Add host</Button>} />
}

function HostsTable({ hosts }: { hosts: BoardHost[] }) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Status</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>Platform</TableHead>
            <TableHead>Modules</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {hosts.map((h) => (
            <TableRow key={h.id}>
              <TableCell>
                <div className="flex flex-wrap items-center gap-2">
                  <Link to={`/hosts/${h.id}`} className="font-medium break-all underline-offset-4 hover:underline">
                    {h.name}
                  </Link>
                  {h.isLocal && (
                    <Badge variant="secondary" size="sm">
                      This controller
                    </Badge>
                  )}
                  {h.state === "pending" && (
                    <Badge variant="outline" size="sm">
                      Pending
                    </Badge>
                  )}
                  {h.state === "revoked" && (
                    <Badge variant="destructive" size="sm">
                      Revoked
                    </Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <div className="flex flex-col items-start gap-1">
                  <span>{h.online ? "Online" : "Offline"}</span>
                  <span className="text-xs text-muted-foreground">
                    {h.lastSeenAt === null ? "Never seen" : `Last seen ${formatTime(h.lastSeenAt)}`}
                  </span>
                  {h.ingestionError !== null && (
                    <>
                      <Badge variant="destructive" size="sm">
                        Journal ingestion failing
                      </Badge>
                      <p className="text-xs text-muted-foreground">{h.ingestionError}</p>
                    </>
                  )}
                </div>
              </TableCell>
              <TableCell>
                <div className="flex flex-col items-start gap-1">
                  <span className="break-all">{h.agentVersion ?? "—"}</span>
                  <span className="text-xs text-muted-foreground">
                    {h.protocolVersion === null ? "—" : `protocol ${h.protocolVersion}`}
                  </span>
                  {!h.supported && h.agentVersion !== null && (
                    <Badge variant="destructive" size="sm">
                      Unsupported
                    </Badge>
                  )}
                </div>
              </TableCell>
              <TableCell>{h.os === null || h.arch === null ? "—" : `${h.os} / ${h.arch}`}</TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-1">
                  {h.modules.map((m) => (
                    <Badge key={m.id} variant={m.variant} size="sm">
                      {`${m.id}: ${m.text}`}
                    </Badge>
                  ))}
                </div>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function HostsBoard() {
  return (
    <>
      <Panel title="Loading">
        <HostsHeader />
        <Spinner aria-label="Loading the hosts" />
      </Panel>
      <Panel title="Error">
        <HostsHeader />
        <Alert variant="destructive" role="alert">
          <AlertDescription>The controller did not answer. Try again in a moment.</AlertDescription>
        </Alert>
      </Panel>
      <Panel title="Empty">
        <HostsHeader />
        <EmptyState title="No hosts yet" />
      </Panel>
      <Panel title="Mixed fleet">
        <HostsHeader />
        <HostsTable hosts={MIXED} />
      </Panel>
      <Panel title="Worst case">
        <HostsHeader />
        <HostsTable hosts={WORST} />
      </Panel>
    </>
  )
}

type History = { kind: "rows"; rows: BoardTransaction[] } | { kind: "empty" } | { kind: "offline" } | { kind: "error" }

function HistoryTable({ rows }: { rows: BoardTransaction[] }) {
  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Started</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Version</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Error</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((t) => (
            <TableRow key={t.startedAt}>
              <TableCell>{formatTime(t.startedAt)}</TableCell>
              <TableCell>{t.kind}</TableCell>
              <TableCell className="break-all">{`${t.fromVersion} → ${t.toVersion}`}</TableCell>
              <TableCell>
                <Badge variant={stateVariant(t.state)} size="sm">
                  {t.state}
                </Badge>
              </TableCell>
              <TableCell>{t.error ?? "—"}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function HistoryBody({ history }: { history: History }) {
  switch (history.kind) {
    case "rows":
      return <HistoryTable rows={history.rows} />
    case "empty":
      return <EmptyState title="No updates yet" />
    case "offline":
      return <p className="text-sm text-muted-foreground">The update history is unavailable while the host is offline.</p>
    case "error":
      return (
        <Alert variant="destructive" role="alert">
          <AlertDescription>The agent refused the request: update status is not available.</AlertDescription>
        </Alert>
      )
  }
}

function Overview({ h, history }: { h: BoardHost; history: History }) {
  const state = h.state === "pending" ? "Pending" : h.state === "revoked" ? "Revoked" : "Enrolled"
  const rows: [string, string][] = [
    ["State", state],
    ["Status", `${h.online ? "Online" : "Offline"}, ${h.lastSeenAt === null ? "never seen" : `last seen ${formatTime(h.lastSeenAt)}`}`],
    ["Agent version", h.agentVersion ?? "—"],
    ["Protocol version", h.protocolVersion === null ? "—" : String(h.protocolVersion)],
    ["OS", h.os ?? "—"],
    ["Architecture", h.arch ?? "—"],
    ["Supported", h.agentVersion === null ? "—" : h.supported ? "Yes" : "No"],
  ]
  const removable = h.state === "pending" || h.state === "revoked"
  return (
    <>
      <PageHeader
        breadcrumbs={[{ label: "Hosts", href: "/" }, { label: h.name }]}
        title={h.name}
        tabs={[
          { label: "Overview", value: "overview", href: `/hosts/${h.id}` },
          { label: "Firewall", value: "firewall", href: `/hosts/${h.id}/firewall` },
        ]}
      />
      <div className="flex flex-col gap-4">
        <Card>
          <CardHeader>
            <CardTitle>
              <h3>Host</h3>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <dl className="grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-[max-content_1fr]">
              {rows.map(([label, value]) => (
                <div key={label} className="contents">
                  <dt className="text-muted-foreground">{label}</dt>
                  <dd className="break-all">{value}</dd>
                </div>
              ))}
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>
              <h3>Updates</h3>
            </CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex flex-wrap gap-2">
              {h.state === "enrolled" && <Button>Update now</Button>}
              <Button variant="outline">Change update policy</Button>
            </div>
            <HistoryBody history={history} />
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>
              <h3>Enrollment and removal</h3>
            </CardTitle>
          </CardHeader>
          <CardContent>
            <div className="flex flex-wrap gap-2">
              {removable && <Button variant="outline">New enrollment token</Button>}
              {h.state !== "revoked" && <Button variant="destructive">Revoke host</Button>}
              {removable && <Button variant="destructive">Delete host</Button>}
            </div>
          </CardContent>
        </Card>
      </div>
    </>
  )
}

function HostOverviewBoard() {
  const online = host({})
  return (
    <>
      <Panel title="Enrolled, online">
        <Overview h={online} history={{ kind: "rows", rows: HISTORY }} />
      </Panel>
      <Panel title="Pending">
        <Overview h={PENDING} history={{ kind: "offline" }} />
      </Panel>
      <Panel title="Revoked">
        <Overview h={REVOKED} history={{ kind: "offline" }} />
      </Panel>
      <Panel title="Offline">
        <Overview h={OFFLINE} history={{ kind: "offline" }} />
      </Panel>
      <Panel title="History empty">
        <Overview h={online} history={{ kind: "empty" }} />
      </Panel>
      <Panel title="History error">
        <Overview h={online} history={{ kind: "error" }} />
      </Panel>
    </>
  )
}

function RolloutsHeader() {
  return <PageHeader title="Rollouts" actions={<Button>Start rollout</Button>} />
}

function RolloutCard({ r }: { r: BoardRollout }) {
  return (
    <Card>
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-2">
          <CardTitle>
            <h3 className="break-all">{`${r.version} (${r.channel})`}</h3>
          </CardTitle>
          <Badge variant={stateVariant(r.state)} size="sm">
            {r.state}
          </Badge>
        </div>
        {(r.state === "running" || r.state === "draining") && <Button variant="outline">Cancel rollout</Button>}
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <p className="text-xs text-muted-foreground">{`Created ${formatTime(r.createdAt)} · updated ${formatTime(r.updatedAt)}`}</p>
        {r.error !== null && (
          <Alert variant="destructive">
            <AlertDescription>{r.error}</AlertDescription>
          </Alert>
        )}
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>#</TableHead>
                <TableHead>Host</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Error</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {r.hosts.map((h, i) => (
                <TableRow key={h.hostId}>
                  <TableCell>{i + 1}</TableCell>
                  <TableCell className="break-all">{h.name ?? h.hostId}</TableCell>
                  <TableCell>
                    <Badge variant={stateVariant(h.state)} size="sm">
                      {h.state}
                    </Badge>
                  </TableCell>
                  <TableCell>{h.error ?? "—"}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </CardContent>
    </Card>
  )
}

function rollout(over: Partial<BoardRollout>): BoardRollout {
  return {
    id: "r1",
    version: "1.1.0",
    channel: "stable",
    state: "running",
    error: null,
    createdAt: "2026-11-11T07:00:00Z",
    updatedAt: "2026-11-11T07:04:00Z",
    hosts: [
      { hostId: "local", name: "controller", state: "succeeded", error: null },
      { hostId: "web-1", name: "web-1", state: "updating", error: null },
      { hostId: "web-2", name: "web-2", state: "waiting", error: null },
      { hostId: "01JE00000000000000000000Z9", name: null, state: "waiting", error: null },
    ],
    ...over,
  }
}

const WORST_ROLLOUTS: BoardRollout[] = Array.from({ length: 20 }, (_, r) =>
  rollout({
    id: `r${r}`,
    version: r === 0 ? LONG_VERSION : `1.${20 - r}.0`,
    state: r === 0 ? "running" : "succeeded",
    hosts: Array.from({ length: 50 }, (_, i) => ({
      hostId: `h${i}`,
      name: i === 0 ? LONG_NAME : `web-${i + 1}`,
      state: r === 0 && i > 0 ? "waiting" : "succeeded",
      error: null,
    })),
  }),
)

function RolloutsBoard() {
  return (
    <>
      <Panel title="Loading">
        <RolloutsHeader />
        <Spinner aria-label="Loading the rollouts" />
      </Panel>
      <Panel title="Empty">
        <RolloutsHeader />
        <EmptyState title="No rollouts yet" action={<Button>Start rollout</Button>} />
      </Panel>
      <Panel title="Running">
        <RolloutsHeader />
        <RolloutCard r={rollout({})} />
      </Panel>
      <Panel title="Failed">
        <RolloutsHeader />
        <RolloutCard
          r={rollout({
            state: "failed",
            error: "web-1 failed its update, so the rollout stopped",
            hosts: [
              { hostId: "local", name: "controller", state: "succeeded", error: null },
              { hostId: "web-1", name: "web-1", state: "failed", error: "the new agent did not report healthy within 60 seconds" },
              { hostId: "web-2", name: "web-2", state: "skipped", error: null },
            ],
          })}
        />
      </Panel>
      <Panel title="Cancelled">
        <RolloutsHeader />
        <RolloutCard
          r={rollout({
            state: "cancelled",
            hosts: [
              { hostId: "local", name: "controller", state: "succeeded", error: null },
              { hostId: "web-1", name: "web-1", state: "cancelled", error: null },
            ],
          })}
        />
        <RolloutCard
          r={rollout({
            id: "r2",
            state: "draining",
            hosts: [
              { hostId: "web-1", name: "web-1", state: "updating", error: null },
              { hostId: "web-2", name: "web-2", state: "cancelled", error: null },
            ],
          })}
        />
      </Panel>
      <Panel title="Worst case">
        <RolloutsHeader />
        {/* Uncapped, 20 rollouts of 50 hosts make the board about 35,000 px
            tall, and Chromium captures nothing past 16,384 px. */}
        <div className="flex max-h-[4000px] flex-col gap-4 overflow-y-auto">
          {WORST_ROLLOUTS.map((r) => (
            <RolloutCard key={r.id} r={r} />
          ))}
        </div>
      </Panel>
    </>
  )
}

const BOARDS: Record<string, () => ReactNode> = {
  hosts: HostsBoard,
  "host-overview": HostOverviewBoard,
  rollouts: RolloutsBoard,
}

const params = new URLSearchParams(location.search)
const board = params.get("board") ?? "hosts"
const theme = params.get("theme") === "light" ? "light" : "dark"
localStorage.setItem("mode", theme)
document.documentElement.dataset.mode = theme
// darkraise-ui pins html and body to the viewport for its app shell, so a
// board taller than the viewport would be screenshotted blank below the fold.
for (const el of [document.documentElement, document.body]) {
  el.style.height = "auto"
  el.style.overflow = "visible"
}

const Board = BOARDS[board] ?? HostsBoard

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <MemoryRouter>
      <Providers client={new QueryClient()}>
        <main className="bg-background p-6 text-foreground">
          <section data-testid={`board-${board}`} className="mx-auto flex max-w-6xl flex-col gap-6 bg-background">
            <Board />
          </section>
        </main>
      </Providers>
    </MemoryRouter>
  </StrictMode>,
)
