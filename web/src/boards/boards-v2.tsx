import type { ReactNode } from "react"
import {
  ArrowRight,
  Clock,
  Container,
  Cpu,
  Download,
  Lock,
  RefreshCw,
  RotateCcw,
  Search,
  Server,
  ShieldCheck,
  Trash2,
} from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Switch } from "darkraise-ui/components/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "darkraise-ui/components/tabs"
import { ToggleGroup, ToggleGroupItem } from "darkraise-ui/components/toggle-group"
import { Gauge } from "@/components/visual/gauge"
import { InfoTip } from "@/components/visual/info-tip"
import { ModuleIcon } from "@/components/visual/module-icon"
import { ResourceCard } from "@/components/visual/resource-card"
import { SectionHeader } from "@/components/visual/section-header"
import { StatTile } from "@/components/visual/stat-tile"
import type { Status } from "@/components/visual/status"
import { StatusDonut } from "@/components/visual/status-donut"
import { StatusDot } from "@/components/visual/status-dot"
import { StatusPill } from "@/components/visual/status-pill"
import { Stepper } from "@/components/visual/stepper"
import { UsageBar } from "@/components/visual/usage-bar"

// The v2 boards draw the revamped screens and the system module's surfaces
// from the visual vocabulary, with local fixture data only.

const STATUSES: { status: Status; label: string }[] = [
  { status: "ok", label: "Running" },
  { status: "warn", label: "Reboot needed" },
  { status: "error", label: "Offline" },
  { status: "info", label: "Applying" },
  { status: "off", label: "Disabled" },
]

const MODULES = ["hostinfo", "firewall", "docker", "tailscale", "tailnet", "derp", "npm", "cloudflare", "panel", "system"]
const KINDS = ["container", "image", "volume", "network", "package"]
const LONG_HOST = `web-${"x".repeat(60)}`
const LONG_PKG = `lib${"x".repeat(60)}-dev-7`

function Panel({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-4 rounded-lg border border-border p-4">
      <h2 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">{title}</h2>
      {children}
    </div>
  )
}

function Grid({ children }: { children: ReactNode }) {
  return <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">{children}</div>
}

function Tile({ title, status, children, actions }: { title: string; status?: { status: Status; label: string }; children: ReactNode; actions?: ReactNode }) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 space-y-0 p-4 pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        {status && <StatusPill status={status.status} label={status.label} />}
      </CardHeader>
      <CardContent className="flex flex-col gap-3 p-4 pt-0">
        {children}
        {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
      </CardContent>
    </Card>
  )
}

function SkeletonTiles({ count }: { count: number }) {
  return (
    <Grid>
      {Array.from({ length: count }, (_, i) => (
        <Card key={i}>
          <CardContent className="flex flex-col gap-3 p-4">
            <Skeleton className="h-4 w-24" />
            <Skeleton className="h-8 w-16" />
            <Skeleton className="h-2 w-full" />
          </CardContent>
        </Card>
      ))}
    </Grid>
  )
}

export function DesignLanguageBoard() {
  return (
    <>
      <SectionHeader icon={Server} title="Design language" status={{ status: "ok", label: "Glass · darkpanel" }} actions={<Button>Primary action</Button>} />
      <Panel title="Status">
        <div className="flex flex-wrap items-center gap-4">
          {STATUSES.map((s) => (
            <span key={s.status} className="flex items-center gap-2 text-sm">
              <StatusDot status={s.status} label={s.label} />
              {s.label}
            </span>
          ))}
        </div>
        <div className="flex flex-wrap gap-2">
          {STATUSES.map((s) => (
            <StatusPill key={s.status} status={s.status} label={s.label} />
          ))}
        </div>
      </Panel>
      <Panel title="Figures">
        <Grid>
          <StatTile icon={Download} figure={412} caption="Upgradable" />
          <StatTile icon={ShieldCheck} figure={37} caption="Security" />
          <StatTile icon={Cpu} figure="0.42" caption="Load per core" />
          <StatTile icon={Clock} figure="41 d 3 h" caption="Uptime" />
        </Grid>
      </Panel>
      <Panel title="Gauges and bars">
        <div className="flex flex-wrap gap-8">
          <Gauge value={0} max={2} label="Swap" unit="GiB" />
          <Gauge value={6.2} max={8} label="Memory" unit="GiB" />
          <Gauge value={9} max={8} label="Over limit" unit="GiB" />
        </div>
        <div className="grid max-w-md gap-3">
          <UsageBar used={0} total={100} label="Empty" unit="GiB" />
          <UsageBar used={41} total={100} label="Disk" unit="GiB" />
          <UsageBar used={100} total={100} label="Full" unit="GiB" />
        </div>
      </Panel>
      <Panel title="Donut">
        <div className="flex flex-wrap gap-8">
          <StatusDonut
            label="Containers"
            segments={[
              { status: "ok", label: "Running", value: 12 },
              { status: "warn", label: "Restarting", value: 1 },
              { status: "error", label: "Exited", value: 2 },
              { status: "off", label: "Stopped", value: 4 },
            ]}
          />
          <StatusDonut label="No containers" segments={[]} />
        </div>
      </Panel>
      <Panel title="Resource cards">
        <Grid>
          <ResourceCard
            icon={Server}
            title="web-1"
            status={{ status: "ok", label: "Online" }}
            figures={<UsageBar used={3.1} total={8} label="Memory" unit="GiB" />}
            actions={<Button variant="outline" size="sm">Open</Button>}
          />
          <ResourceCard icon={Server} title={LONG_HOST} status={{ status: "error", label: "Offline" }} actions={<Button variant="outline" size="sm">Open</Button>} />
        </Grid>
      </Panel>
      <Panel title="Stepper">
        <Stepper steps={[{ id: "plan", label: "Plan" }, { id: "confirm", label: "Confirm" }, { id: "apply", label: "Apply" }]} current="confirm" />
        <Stepper steps={[{ id: "plan", label: "Plan" }, { id: "confirm", label: "Confirm" }, { id: "apply", label: "Apply" }]} current="apply" failed />
      </Panel>
      <Panel title="Info tip and live status">
        <div className="flex items-center gap-2 text-sm">
          Held back
          <InfoTip label="Why held">Phased updates reach this host later.</InfoTip>
          <StatusPill status="info" label="Rebooting" live />
        </div>
      </Panel>
      <Panel title="Module icons">
        <div className="flex flex-wrap gap-4">
          {[...MODULES, ...KINDS].map((m) => (
            <ModuleIcon key={m} module={m} />
          ))}
          <ModuleIcon module="docker" dimmed />
        </div>
      </Panel>
    </>
  )
}

interface CardHost {
  name: string
  online: boolean
  os: string
  arch: string
  mem?: [number, number]
  disk?: [number, number]
  readFailed?: boolean
  lastSeen?: string
  modules: { id: string; dimmed?: boolean }[]
}

const ALL_MODULES = MODULES.map((id) => ({ id }))

const WEB1: CardHost = { name: "web-1", online: true, os: "Ubuntu 24.04", arch: "amd64", mem: [3.1, 8], disk: [41, 100], modules: ALL_MODULES }

const FLEET: CardHost[] = [
  WEB1,
  { name: "db-1", online: false, os: "Debian 12", arch: "arm64", lastSeen: "2026-11-10 22:15", modules: [{ id: "hostinfo" }, { id: "firewall" }, { id: "system" }] },
  { name: "edge-2", online: true, os: "Ubuntu 26.04", arch: "amd64", readFailed: true, modules: [{ id: "hostinfo" }, { id: "docker", dimmed: true }, { id: "system" }] },
  { name: LONG_HOST, online: true, os: "Debian 13", arch: "arm64", mem: [7.9, 8], disk: [97, 100], modules: ALL_MODULES },
]

function HostCard({ h }: { h: CardHost }) {
  return (
    <ResourceCard
      icon={Server}
      title={h.name}
      status={h.online ? { status: "ok", label: "Online" } : { status: "error", label: "Offline" }}
      actions={<Button variant="outline" size="sm">Open</Button>}
      figures={
        <>
          <div className="flex flex-wrap gap-1">
            <Badge variant="outline">{h.os}</Badge>
            <Badge variant="outline">{h.arch}</Badge>
          </div>
          {!h.online && <span className="text-muted-foreground text-sm">Last seen {h.lastSeen}</span>}
          {h.readFailed && (
            <span className="flex items-center gap-1 text-sm">
              <StatusDot status="error" label="Host information unavailable" />
              Usage unavailable
              <InfoTip label="Why unavailable">The host information read timed out after 5 seconds.</InfoTip>
            </span>
          )}
          {h.online && h.mem && <UsageBar used={h.mem[0]} total={h.mem[1]} label="Memory" unit="GiB" />}
          {h.online && h.disk && <UsageBar used={h.disk[0]} total={h.disk[1]} label="Disk" unit="GiB" />}
        </>
      }
    >
      <div className="flex flex-wrap gap-2">
        {h.modules.map((m) => (
          <ModuleIcon key={m.id} module={m.id} dimmed={m.dimmed} />
        ))}
      </div>
    </ResourceCard>
  )
}

function HostsV2Header({ view }: { view: "cards" | "table" }) {
  return (
    <SectionHeader
      icon={Server}
      title="Hosts"
      actions={
        <>
          <ToggleGroup type="single" value={view} aria-label="View">
            <ToggleGroupItem value="cards">Cards</ToggleGroupItem>
            <ToggleGroupItem value="table">Table</ToggleGroupItem>
          </ToggleGroup>
          <Button>Add host</Button>
        </>
      }
    />
  )
}

const MANY: CardHost[] = Array.from({ length: 50 }, (_, i) => ({
  name: `node-${String(i + 1).padStart(2, "0")}`,
  online: i % 9 !== 4,
  os: i % 2 === 0 ? "Ubuntu 24.04" : "Debian 12",
  arch: i % 3 === 0 ? "arm64" : "amd64",
  mem: [(i % 8) + 0.5, 8],
  disk: [(i * 7) % 100, 100],
  lastSeen: "2026-11-18 09:40",
  modules: [{ id: "hostinfo" }, { id: "system" }, { id: "docker", dimmed: i % 4 === 0 }],
}))

export function HostsV2Board() {
  return (
    <>
      <Panel title="Loading">
        <HostsV2Header view="cards" />
        <SkeletonTiles count={4} />
      </Panel>
      <Panel title="Card grid">
        <HostsV2Header view="cards" />
        <Grid>
          {FLEET.map((h) => (
            <HostCard key={h.name} h={h} />
          ))}
        </Grid>
      </Panel>
      <Panel title="One host">
        <HostsV2Header view="cards" />
        <Grid>
          <HostCard h={WEB1} />
        </Grid>
      </Panel>
      <Panel title="Table view">
        <HostsV2Header view="table" />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Host</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>System</TableHead>
              <TableHead>Memory</TableHead>
              <TableHead>Modules</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {FLEET.map((h) => (
              <TableRow key={h.name}>
                <TableCell className="max-w-48 truncate font-medium">{h.name}</TableCell>
                <TableCell>
                  <StatusPill status={h.online ? "ok" : "error"} label={h.online ? "Online" : "Offline"} />
                </TableCell>
                <TableCell>{`${h.os} · ${h.arch}`}</TableCell>
                <TableCell className="min-w-40">
                  {h.mem ? <UsageBar used={h.mem[0]} total={h.mem[1]} label="Memory" unit="GiB" /> : <span className="text-muted-foreground">—</span>}
                </TableCell>
                <TableCell>
                  <div className="flex gap-2">
                    {h.modules.map((m) => (
                      <ModuleIcon key={m.id} module={m.id} dimmed={m.dimmed} />
                    ))}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Panel>
      <Panel title="50 hosts">
        <Grid>
          {MANY.map((h) => (
            <HostCard key={h.name} h={h} />
          ))}
        </Grid>
      </Panel>
    </>
  )
}

function OverviewHeader({ name, status }: { name: string; status: { status: Status; label: string } }) {
  return <SectionHeader icon={Server} title={name} status={status} />
}

function OverviewTiles({ offline, failed }: { offline?: boolean; failed?: boolean }) {
  const unavailable = (label: string) =>
    failed ? (
      <span className="flex items-center gap-1 text-sm">
        <StatusDot status="error" label={`${label} unavailable`} />
        Unavailable
        <InfoTip label={`Why ${label.toLowerCase()} is unavailable`}>The host information read timed out after 5 seconds.</InfoTip>
      </span>
    ) : (
      <span className="text-muted-foreground text-sm">Last seen 2026-11-10 22:15</span>
    )
  const down = offline || failed
  return (
    <>
      <Grid>
        <Tile title="Memory">{down ? unavailable("Memory") : <Gauge value={6.2} max={8} label="Memory" unit="GiB" />}</Tile>
        <Tile title="Disk">{down ? unavailable("Disk") : <Gauge value={41} max={100} label="Disk" unit="GiB" />}</Tile>
        <Tile title="Swap">{down ? unavailable("Swap") : <Gauge value={0.4} max={2} label="Swap" unit="GiB" />}</Tile>
        <div className="flex flex-col gap-4">
          <StatTile icon={Cpu} figure={down ? "—" : "0.42"} caption="Load per core" />
          <StatTile icon={Clock} figure={down ? "—" : "41 d 3 h"} caption="Uptime" />
        </div>
      </Grid>
      <Grid>
        <Tile title="Updates" status={{ status: "warn", label: "Reboot needed" }}>
          <div className="flex items-baseline gap-4">
            <span className="font-mono text-2xl font-semibold tabular-nums">12</span>
            <span className="text-muted-foreground text-sm">pending</span>
            <span className="font-mono text-2xl font-semibold tabular-nums">3</span>
            <span className="text-muted-foreground text-sm">security</span>
          </div>
        </Tile>
        <Tile title="Docker">
          <StatusDonut
            label="Containers"
            segments={[
              { status: "ok", label: "Running", value: 9 },
              { status: "error", label: "Exited", value: 1 },
              { status: "off", label: "Stopped", value: 3 },
            ]}
          />
        </Tile>
        <div className="flex flex-col gap-4">
          <Tile title="Firewall" status={offline ? { status: "off", label: "Last known" } : { status: "ok", label: "Active" }}>
            <span className="flex items-center gap-2 text-sm">
              <ShieldCheck aria-hidden className="text-success-text size-5" />
              <span className="font-mono tabular-nums">24</span> rules
            </span>
          </Tile>
          <Tile title="Tailscale" status={offline ? { status: "off", label: "Unknown" } : { status: "ok", label: "Connected" }}>
            <span className="font-mono text-sm tabular-nums">100.101.12.4</span>
          </Tile>
        </div>
        <Tile title="Agent" status={{ status: "info", label: "Update available" }} actions={<><Button size="sm" disabled={offline}>Update agent</Button><Button size="sm" variant="outline">Change policy</Button></>}>
          <span className="text-sm">
            <span className="font-mono tabular-nums">1.0.0-rc.2</span>
            <ArrowRight aria-label="to" className="mx-1 inline size-3.5" />
            <span className="font-mono tabular-nums">1.0.0</span>
          </span>
          <span className="text-muted-foreground text-sm">Policy: weekly window, Sunday 03:00</span>
        </Tile>
      </Grid>
    </>
  )
}

export function HostOverviewV2Board() {
  return (
    <>
      <Panel title="Loading">
        <OverviewHeader name="web-1" status={{ status: "off", label: "Loading" }} />
        <SkeletonTiles count={4} />
        <SkeletonTiles count={4} />
      </Panel>
      <Panel title="Online">
        <OverviewHeader name="web-1" status={{ status: "ok", label: "Online" }} />
        <OverviewTiles />
      </Panel>
      <Panel title="Offline">
        <OverviewHeader name="db-1" status={{ status: "error", label: "Offline" }} />
        <OverviewTiles offline />
      </Panel>
      <Panel title="Host information read fails">
        <OverviewHeader name={LONG_HOST} status={{ status: "ok", label: "Online" }} />
        <OverviewTiles failed />
      </Panel>
    </>
  )
}

const UPGRADABLE: { name: string; from: string; to: string; security: boolean }[] = [
  { name: "openssl", from: "3.0.13-0ubuntu3.4", to: "3.0.13-0ubuntu3.5", security: true },
  { name: "linux-image-generic", from: "6.8.0-45.45", to: "6.8.0-48.48", security: true },
  { name: LONG_PKG, from: "2.4.1-1", to: "2.4.1-1+deb12u1", security: true },
  { name: "tzdata", from: "2024a-3ubuntu1", to: "2025b-0ubuntu0.24.04", security: false },
  { name: "ca-certificates", from: "20240203", to: "20240203~24.04.1", security: false },
]

function SystemOverview() {
  return (
    <Grid>
      <Tile
        title="Updates"
        status={{ status: "warn", label: "412 upgradable" }}
        actions={
          <>
            <Button size="sm" variant="outline">
              <RefreshCw aria-hidden />
              Refresh
            </Button>
            <Button size="sm">Upgrade all</Button>
          </>
        }
      >
        <div className="flex items-baseline gap-3">
          <span className="font-mono text-2xl font-semibold tabular-nums">412</span>
          <span className="text-muted-foreground text-sm">pending</span>
          <span className="font-mono text-2xl font-semibold tabular-nums">37</span>
          <span className="text-muted-foreground text-sm">security</span>
        </div>
        <span className="text-muted-foreground text-xs">Refreshed 2026-11-19 11:40</span>
      </Tile>
      <Tile title="Reboot" status={{ status: "warn", label: "Reboot needed" }} actions={<Button size="sm" variant="outline">Reboot</Button>}>
        <span className="text-sm">
          Kernel <span className="font-mono tabular-nums">6.8.0-45</span>
          <ArrowRight aria-label="to" className="mx-1 inline size-3.5" />
          <span className="font-mono tabular-nums">6.8.0-48</span>
        </span>
        <span className="text-muted-foreground text-sm">2 packages · 3 services to restart</span>
      </Tile>
      <Tile title="Automatic updates" status={{ status: "ok", label: "On" }}>
        <label className="flex items-center gap-2 text-sm">
          <Switch defaultChecked aria-label="Automatic security updates" />
          Security updates
        </label>
      </Tile>
      <Tile
        title="Docker Engine"
        status={{ status: "ok", label: "Running" }}
        actions={
          <>
            <Button size="sm">Update</Button>
            <Button size="sm" variant="outline">Uninstall</Button>
          </>
        }
      >
        <span className="text-sm">
          docker-ce <span className="font-mono tabular-nums">27.3.1</span>
        </span>
        <StatusPill status="info" label="Update available" />
      </Tile>
    </Grid>
  )
}

function UpdatesTab() {
  const group = (title: string, rows: typeof UPGRADABLE, tip?: string) => (
    <div className="flex flex-col gap-1">
      <h3 className="flex items-center gap-1 text-sm font-semibold">
        {title}
        {tip && <InfoTip label={`About ${title.toLowerCase()}`}>{tip}</InfoTip>}
      </h3>
      <Table>
        <TableBody>
          {rows.map((r) => (
            <TableRow key={r.name}>
              <TableCell className="w-8">
                <Checkbox aria-label={`Select ${r.name}`} defaultChecked={r.security} />
              </TableCell>
              <TableCell className="max-w-80 font-mono text-sm break-all whitespace-normal">{r.name}</TableCell>
              <TableCell className="font-mono text-sm tabular-nums whitespace-normal">
                {r.from}
                <ArrowRight aria-label="to" className="mx-1 inline size-3.5" />
                {r.to}
              </TableCell>
              <TableCell>{r.security && <StatusPill status="warn" label="Security" />}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between gap-2">
        <span className="text-muted-foreground text-sm">3 of 412 selected</span>
        <Button size="sm">Upgrade selected</Button>
      </div>
      {group("Security", UPGRADABLE.filter((r) => r.security))}
      {group("Other updates", UPGRADABLE.filter((r) => !r.security))}
      <div className="flex flex-col gap-1">
        <h3 className="text-sm font-semibold">Will be removed</h3>
        <div className="flex flex-wrap gap-2">
          <Badge variant="destructive-subtle">linux-image-6.8.0-31-generic</Badge>
          <Badge variant="destructive-subtle">libpython3.11</Badge>
        </div>
      </div>
      <div className="flex flex-col gap-1">
        <h3 className="flex items-center gap-1 text-sm font-semibold">
          Held back
          <InfoTip label="About held back">Held packages stay until unheld. Phased updates reach this host later.</InfoTip>
        </h3>
        <div className="flex flex-wrap gap-2">
          <StatusPill status="off" label="nginx · held" />
          <StatusPill status="off" label="systemd · phased" />
        </div>
      </div>
    </div>
  )
}

function PackagesTab() {
  const installed = [
    { name: "openssh-server", version: "1:9.6p1-3ubuntu13.5", protected: "Keeps remote access" },
    { name: "curl", version: "8.5.0-2ubuntu10.4", protected: null },
    { name: LONG_PKG, version: "2.4.1-1", protected: null },
    { name: "docker-ce", version: "5:27.3.1-1~ubuntu.24.04~noble", protected: "Removed only by Docker uninstall" },
  ]
  return (
    <div className="flex flex-col gap-4">
      <Input type="search" aria-label="Search the apt sources" placeholder="Search packages" prefix={<Search aria-hidden className="size-4" />} defaultValue="htop" />
      <Table>
        <TableBody>
          <TableRow>
            <TableCell className="font-mono text-sm">htop</TableCell>
            <TableCell className="font-mono text-sm tabular-nums">3.3.0-4build1</TableCell>
            <TableCell className="text-end">
              <Button size="sm" variant="outline">Install</Button>
            </TableCell>
          </TableRow>
          <TableRow>
            <TableCell className="font-mono text-sm">btop</TableCell>
            <TableCell className="font-mono text-sm tabular-nums">1.3.0-1</TableCell>
            <TableCell className="text-end">
              <StatusPill status="ok" label="Installed" />
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">
          Installed <span className="text-muted-foreground font-mono tabular-nums">3000</span>
        </h3>
        <Input aria-label="Filter installed packages" placeholder="Filter" className="max-w-56" />
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Package</TableHead>
            <TableHead>Version</TableHead>
            <TableHead className="text-end">Action</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {installed.map((p) => (
            <TableRow key={p.name}>
              <TableCell className="max-w-80 font-mono text-sm break-all whitespace-normal">{p.name}</TableCell>
              <TableCell className="font-mono text-sm tabular-nums whitespace-normal">{p.version}</TableCell>
              <TableCell className="text-end">
                {p.protected ? (
                  <span className="inline-flex items-center gap-1 text-sm">
                    <Lock aria-label="Protected" className="text-muted-foreground size-4" />
                    <InfoTip label={`Why ${p.name} is protected`}>{p.protected}</InfoTip>
                  </span>
                ) : (
                  <Button size="sm" variant="outline">
                    <Trash2 aria-hidden />
                    Remove
                  </Button>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function SystemHeader() {
  return <SectionHeader icon={Server} title="System" status={{ status: "warn", label: "Reboot needed" }} />
}

export function SystemBoard() {
  return (
    <>
      <Panel title="Tabs">
        <SystemHeader />
        <Tabs defaultValue="overview">
          <TabsList aria-label="System">
            <TabsTrigger value="overview">Overview</TabsTrigger>
            <TabsTrigger value="updates">Updates</TabsTrigger>
            <TabsTrigger value="packages">Packages</TabsTrigger>
          </TabsList>
          <TabsContent value="overview">
            <SystemOverview />
          </TabsContent>
        </Tabs>
      </Panel>
      <Panel title="Overview">
        <SystemOverview />
      </Panel>
      <Panel title="Up to date">
        <Grid>
          <Tile title="Updates" status={{ status: "ok", label: "Up to date" }} actions={<Button size="sm" variant="outline">Refresh</Button>}>
            <span className="text-muted-foreground text-xs">Refreshed 2026-11-19 11:40</span>
          </Tile>
        </Grid>
      </Panel>
      <Panel title="Packages interrupted">
        <Grid>
          <Tile title="Updates" status={{ status: "error", label: "Interrupted" }}>
            <Alert variant="destructive">
              <AlertTitle as="h3">Package installation was interrupted</AlertTitle>
              <AlertDescription>
                <Button size="sm" variant="outline">Repair packages</Button>
              </AlertDescription>
            </Alert>
          </Tile>
        </Grid>
      </Panel>
      <Panel title="Failed sources">
        <Grid>
          <Tile title="Updates" status={{ status: "warn", label: "12 upgradable" }}>
            <Alert variant="warning">
              <AlertTitle as="h3">2 sources failed at the last refresh</AlertTitle>
              <AlertDescription className="font-mono text-xs break-all">download.docker.com, ppa.launchpadcontent.net</AlertDescription>
            </Alert>
          </Tile>
        </Grid>
      </Panel>
      <Panel title="Apt busy and failed change">
        <Grid>
          <Tile title="Updates" status={{ status: "warn", label: "Busy" }}>
            <Alert variant="warning">
              <AlertTitle as="h3">Another package manager is running</AlertTitle>
              <AlertDescription>
                <Button size="sm" variant="outline">
                  <RotateCcw aria-hidden />
                  Retry
                </Button>
              </AlertDescription>
            </Alert>
          </Tile>
          <Tile title="Last change" status={{ status: "error", label: "Failed" }}>
            <span className="flex items-center gap-1 text-sm">
              Package <span className="font-mono">nginx-common</span> failed to configure
              <InfoTip label="apt output">dpkg: error processing package nginx-common (--configure)</InfoTip>
            </span>
          </Tile>
        </Grid>
      </Panel>
      <Panel title="Offline host">
        <Grid>
          <Tile title="Updates" status={{ status: "off", label: "Host offline" }} actions={<Button size="sm" disabled>Upgrade all</Button>}>
            <span className="text-muted-foreground text-sm">Last seen 2026-11-10 22:15</span>
          </Tile>
        </Grid>
      </Panel>
      <Panel title="Agent too old">
        <EmptyState icon={<Server aria-hidden />} title="Agent update needed" description="This agent predates the system module." action={<Button>Update agent</Button>} />
      </Panel>
      <Panel title="Updates (worst case)">
        <UpdatesTab />
      </Panel>
      <Panel title="Packages (worst case)">
        <PackagesTab />
      </Panel>
    </>
  )
}

function DockerHeader({ status }: { status: { status: Status; label: string } }) {
  return <SectionHeader icon={Container} title="Docker" status={status} />
}

const INSTALL_STEPS = [
  { id: "check", label: "Check" },
  { id: "packages", label: "Install packages" },
  { id: "firewall", label: "Firewall integration" },
  { id: "confirm", label: "Confirm" },
]

export function DockerInstallBoard() {
  return (
    <>
      <Panel title="Absent">
        <DockerHeader status={{ status: "off", label: "Not installed" }} />
        <EmptyState icon={<Container aria-hidden />} title="Docker is not installed" description="Install Docker Engine from Docker's apt repository." action={<Button>Install Docker</Button>} />
      </Panel>
      <Panel title="Distribution Docker present">
        <DockerHeader status={{ status: "warn", label: "Distribution package" }} />
        <Alert variant="warning">
          <AlertTitle as="h3">Replace the distribution packages by hand first</AlertTitle>
          <AlertDescription className="flex flex-col gap-2">
            <span className="flex flex-wrap gap-2">
              <Badge variant="warning-subtle">docker.io</Badge>
              <Badge variant="warning-subtle">containerd</Badge>
              <Badge variant="warning-subtle">runc</Badge>
            </span>
            <a className="text-foreground underline" href="#replace-distro-docker">
              How to replace them
            </a>
          </AlertDescription>
        </Alert>
      </Panel>
      <Panel title="Daemon stopped">
        <DockerHeader status={{ status: "error", label: "Daemon stopped" }} />
        <span className="flex items-center gap-1 text-sm">
          <StatusPill status="error" label="Daemon stopped" />
          <InfoTip label="Why Docker is unavailable">The Docker socket did not answer. Start the docker service on the host.</InfoTip>
        </span>
      </Panel>
      <Panel title="Installing">
        <DockerHeader status={{ status: "info", label: "Installing" }} />
        <Stepper steps={INSTALL_STEPS} current="packages" />
        <StatusPill status="info" label="Installing docker-ce" live />
      </Panel>
      <Panel title="Awaiting firewall confirmation">
        <DockerHeader status={{ status: "info", label: "Awaiting confirmation" }} />
        <Stepper steps={INSTALL_STEPS} current="confirm" />
        <div className="flex flex-wrap items-center gap-3">
          <span className="flex items-center gap-1 text-sm">
            <Clock aria-hidden className="size-4" />
            Reverts in <span className="font-mono tabular-nums">14:32</span>
          </span>
          <Button>Keep</Button>
          <Button variant="outline">Revert</Button>
        </div>
      </Panel>
      <Panel title="Rolled back">
        <DockerHeader status={{ status: "off", label: "Not installed" }} />
        <Alert variant="warning">
          <AlertTitle as="h3">Install rolled back</AlertTitle>
          <AlertDescription>The firewall step was not confirmed in time.</AlertDescription>
        </Alert>
        <div>
          <Button>Install Docker</Button>
        </div>
      </Panel>
      <Panel title="Failed">
        <DockerHeader status={{ status: "error", label: "Install failed" }} />
        <Stepper steps={INSTALL_STEPS} current="firewall" failed />
        <Alert variant="destructive">
          <AlertTitle as="h3">The firewall refused the Docker integration</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            <span>The install was rolled back.</span>
            <Button size="sm" variant="outline">
              <RotateCcw aria-hidden />
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      </Panel>
      <Panel title="Agent too old">
        <EmptyState icon={<Server aria-hidden />} title="Agent update needed" description="This agent cannot install Docker." action={<Button>Update agent</Button>} />
      </Panel>
      <Panel title="Installed">
        <DockerHeader status={{ status: "ok", label: "Running" }} />
        <Tabs defaultValue="containers">
          <TabsList aria-label="Docker">
            <TabsTrigger value="containers">Containers</TabsTrigger>
            <TabsTrigger value="images">Images</TabsTrigger>
            <TabsTrigger value="volumes">Volumes</TabsTrigger>
            <TabsTrigger value="networks">Networks</TabsTrigger>
          </TabsList>
          {["containers", "images", "volumes", "networks"].map((t) => (
            <TabsContent key={t} value={t} />
          ))}
        </Tabs>
      </Panel>
    </>
  )
}
