import { useLayoutEffect, type ReactNode } from "react"
import { Laptop, MoreHorizontal } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Switch } from "darkraise-ui/components/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { PageHeader } from "darkraise-ui/layout"
import { formatTime } from "@/lib/format"
import { STACKED_BODY, STACKED_CELL, STACKED_FRAME, STACKED_HEAD, STACKED_ROW, STACKED_TABLE } from "@/lib/table"

// The remembered tailnet devices boards (spec UI-1 to UI-5, UX-1 to UX-8):
// every screen and state the spec adds, drawn from kit components with local
// fixture data, so each can be judged against the UI/UX contract before the
// pages are built. Dialogs are drawn open in place, as frames.

// TailnetDevice is C12's type, declared here until the API types gain it.
interface TailnetDevice {
  stableId: string
  nodeName: string
  rememberedAt: string
  lastUsedAt: string
}

// 253 characters, the longest DNS name, in labels of at most 63.
const LONG_NODE = `${"a".repeat(63)}.${"b".repeat(63)}.${"c".repeat(63)}.${"d".repeat(54)}.ts.net`
const LONG_USER = "m".repeat(64)

function device(i: number, nodeName: string): TailnetDevice {
  const day = String(1 + (i % 28)).padStart(2, "0")
  return { stableId: `n${i}CNTRL${i}`, nodeName, rememberedAt: `2026-10-${day}T09:00:00Z`, lastUsedAt: `2026-11-${day}T09:${String(i % 60).padStart(2, "0")}:00Z` }
}

const TWO = [device(1, "laptop.tail1234.ts.net"), device(2, "")]
const FORTY = [device(1, LONG_NODE), device(2, ""), ...Array.from({ length: 38 }, (_, i) => device(i + 3, `host-${i + 3}.tail1234.ts.net`))]

function Tag({ ids }: { ids: string }) {
  return <span className="font-mono text-xs text-muted-foreground">{ids}</span>
}

function State({ id, heading, tag, children }: { id: string; heading: string; tag: string; children: ReactNode }) {
  return (
    <section aria-labelledby={id} className="flex flex-col gap-4 rounded-lg border border-border p-4">
      <h2 id={id} className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
        {heading}
      </h2>
      <Tag ids={tag} />
      {children}
    </section>
  )
}

function firstLabel(nodeName: string) {
  return nodeName.split(".")[0] ?? nodeName
}

// DevicesTable is the UI-2 table the page and the dialog share.
function DevicesTable({ devices }: { devices: TailnetDevice[] }) {
  return (
    <div className={STACKED_FRAME}>
      <Table className={STACKED_TABLE}>
        <TableHeader className={STACKED_HEAD}>
          <TableRow>
            <TableHead>Device</TableHead>
            <TableHead>Remembered</TableHead>
            <TableHead>Last used</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className={STACKED_BODY}>
          {devices.map((d) => (
            <TableRow key={d.stableId} className={STACKED_ROW}>
              <TableCell data-label="Device" className={`${STACKED_CELL} min-w-48 whitespace-normal wrap-anywhere`}>
                <div className="flex flex-col">
                  <span className="font-medium">{d.nodeName === "" ? "Unnamed device" : firstLabel(d.nodeName)}</span>
                  {d.nodeName !== "" && <span className="text-sm text-muted-foreground">{d.nodeName}</span>}
                  <span className="font-mono text-sm text-muted-foreground">{d.stableId}</span>
                </div>
              </TableCell>
              <TableCell data-label="Remembered" className={STACKED_CELL}>
                {formatTime(d.rememberedAt)}
              </TableCell>
              <TableCell data-label="Last used" className={STACKED_CELL}>
                {formatTime(d.lastUsedAt)}
              </TableCell>
              <TableCell className={STACKED_CELL}>
                <Button variant="outline" size="sm" aria-label={`Forget device ${d.nodeName === "" ? d.stableId : firstLabel(d.nodeName)}`}>
                  Forget
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function DevicesPage({ children }: { children: ReactNode }) {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Tailscale devices" description="Devices that may sign you in with Continue as without a code." />
      {children}
    </div>
  )
}

function Empty({ text }: { text: string }) {
  return (
    <>
      <h2 className="sr-only">Device list</h2>
      <EmptyState title="No remembered devices" description={text} />
    </>
  )
}

function LoginFrame({ tag, children }: { tag: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <Tag ids={tag} />
      <Card className="w-full max-w-sm">
        <CardHeader className="flex flex-col items-center gap-3 text-center">
          <CardTitle>
            <span className="text-xl font-medium">Sign in</span>
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
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${prefix}-user`}>Username</Label>
        <Input id={`${prefix}-user`} />
      </div>
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${prefix}-pass`}>Password</Label>
        <Input id={`${prefix}-pass`} type="password" />
      </div>
      <Button>Log in</Button>
    </>
  )
}

function CodeStep({ prefix, line }: { prefix: string; line?: string }) {
  return (
    <>
      {line && <p className="text-sm text-muted-foreground">{line}</p>}
      <div className="flex flex-col gap-1.5">
        <Label htmlFor={`${prefix}-code`}>Authentication code</Label>
        <Input id={`${prefix}-code`} inputMode="numeric" />
      </div>
      <Button>Log in</Button>
      <Button variant="ghost">Back</Button>
    </>
  )
}

const CONTINUE = "Continue as mia (mia@github, from laptop)"

function Boards() {
  return (
    <>
      <State id="td-login-remembered" heading="Login: remembered" tag="UI-1 UX-2 UX-6 remembered">
        <LoginFrame tag="the button signs in without a code">
          <Button variant="secondary">{CONTINUE}</Button>
          <PasswordForm prefix="remembered" />
        </LoginFrame>
      </State>
      <State id="td-login-code-ready" heading="Login: code-ready" tag="UI-1 UX-1 UX-6 code-ready">
        <LoginFrame tag="the same button; it opens the code step">
          <Button variant="secondary">{CONTINUE}</Button>
          <PasswordForm prefix="ready" />
        </LoginFrame>
      </State>
      <State id="td-login-neither" heading="Login: neither" tag="UI-1 UX-3 UX-6 neither">
        <LoginFrame tag="no button; a muted note in its place">
          <p className="text-sm text-muted-foreground">Continue as needs your authenticator code once. Finish account setup or sign in below.</p>
          <PasswordForm prefix="neither" />
        </LoginFrame>
      </State>
      <State id="td-code-step" heading="Code step: remember line" tag="UX-1 UX-2">
        <div className="grid grid-cols-1 gap-6 md:grid-cols-3">
          <LoginFrame tag="UX-1 remember is true">
            <CodeStep prefix="named" line="Enter your code once to remember laptop." />
          </LoginFrame>
          <LoginFrame tag="UX-1 an empty node name">
            <CodeStep prefix="unnamed" line="Enter your code once to remember this device." />
          </LoginFrame>
          <LoginFrame tag="UX-2 the code every time: no remember line">
            <CodeStep prefix="always" />
          </LoginFrame>
        </div>
      </State>
      <State id="td-page-loading" heading="Devices page: loading" tag="UI-2 UX-5 loading, first load">
        <DevicesPage>
          <Spinner aria-label="Loading the devices" />
        </DevicesPage>
      </State>
      <State id="td-page-default" heading="Devices page: default" tag="UI-2 UX-8 a named and an unnamed device">
        <DevicesPage>
          <DevicesTable devices={TWO} />
        </DevicesPage>
      </State>
      <State id="td-page-empty" heading="Devices page: empty" tag="UI-2 UX-5 empty">
        <DevicesPage>
          <Empty text="A device is remembered when you continue as yourself from it with your code." />
        </DevicesPage>
      </State>
      <State id="td-page-error" heading="Devices page: error" tag="UI-2 UX-5 error">
        <DevicesPage>
          <Alert variant="destructive" role="alert">
            <AlertDescription>The controller is not reachable.</AlertDescription>
          </Alert>
        </DevicesPage>
      </State>
      <State id="td-page-worst" heading="Devices page: worst case" tag="UI-2 UX-5 worst case: 40 devices, a 253-character name, an empty name">
        <DevicesPage>
          <DevicesTable devices={FORTY} />
        </DevicesPage>
      </State>
      <State id="td-page-narrow" heading="Devices page: 375 px" tag="UI-2 UX-5 stacked rows, no sideways scroll">
        <div className="w-[375px] max-w-full border border-dashed border-border p-2">
          <DevicesPage>
            <DevicesTable devices={[device(41, LONG_NODE), ...TWO]} />
          </DevicesPage>
        </div>
      </State>
      <State id="td-users-dialog" heading="Users: devices dialog" tag="UI-3 UX-4 UX-5 UX-7">
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-3">
          <div className="flex flex-col gap-2">
            <Tag ids="UI-3 the row menu" />
            <div className="flex w-56 flex-col rounded-md border border-border p-1 text-sm" role="menu" aria-label="Actions for noor">
              <Button variant="ghost" size="sm" className="justify-start" aria-hidden tabIndex={-1}>
                <MoreHorizontal aria-hidden />
                Change role
              </Button>
              <Button variant="ghost" size="sm" className="justify-start" role="menuitem">
                <Laptop aria-hidden />
                Tailscale devices
              </Button>
              <Button variant="ghost" size="sm" className="justify-start" role="menuitem">
                New enrollment link
              </Button>
            </div>
          </div>
          {[
            { tag: "UI-3 a 64-character username", user: LONG_USER, body: <DevicesTable devices={TWO} /> },
            { tag: "UX-5 empty", user: "noor", body: <Empty text="noor has no remembered devices." /> },
          ].map((f) => (
            <div key={f.tag} className="flex flex-col gap-2">
              <Tag ids={f.tag} />
              <Card role="dialog" aria-label={`Tailscale devices of ${f.user}`} className="w-full">
                <CardHeader>
                  <CardTitle>
                    <span className="text-lg font-semibold wrap-anywhere">Tailscale devices of {f.user}</span>
                  </CardTitle>
                </CardHeader>
                <CardContent>{f.body}</CardContent>
              </Card>
            </div>
          ))}
        </div>
      </State>
      <State id="td-sign-in-card" heading="Sign-in: Tailscale card" tag="UI-5">
        <Card className="max-w-xl">
          <CardHeader>
            <CardTitle>Tailscale</CardTitle>
            <CardDescription>Continue as from a node of your tailnet.</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-3">
              <Label htmlFor="ts-on">Allow Continue as</Label>
              <Switch id="ts-on" defaultChecked />
            </div>
            <div className="flex items-start justify-between gap-3">
              <div className="flex flex-col gap-1">
                <Label htmlFor="ts-always">Require the code every time</Label>
                <p className="text-xs text-muted-foreground">When off, each device asks for the code once and is then remembered.</p>
              </div>
              <Switch id="ts-always" />
            </div>
          </CardContent>
        </Card>
      </State>
    </>
  )
}

// TailnetDevicesBoards renders every board of the remembered-devices spec.
export function TailnetDevicesBoards() {
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
  return (
    <main className="min-h-screen bg-background p-6 text-foreground">
      <h1 className="sr-only">Remembered tailnet devices boards</h1>
      <div className="mx-auto flex max-w-6xl flex-col gap-6 bg-background">
        <Boards />
      </div>
    </main>
  )
}
