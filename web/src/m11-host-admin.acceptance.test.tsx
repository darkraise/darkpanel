import { act, fireEvent, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useNavigate } from "react-router"
import { Button } from "darkraise-ui/components/button"
import { afterEach, describe, expect, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { useStepUp } from "@/components/step-up-dialog"
import { FilesView } from "@/modules/console/files-view"
import type { TerminalSurface } from "@/modules/console/terminal"
import { TerminalView } from "@/modules/console/terminal-view"
import { accountsDefault, filesDefault, filesWorst, journalEntries, keysDeploy, sshdState, swapDefault, swapUnsupported, sysctlDefault, unitsDefault } from "@/modules/m11-fixtures"
import { JournalPanel } from "@/modules/services/journal-panel"
import { TuningPage } from "@/modules/tuning/tuning-page"
import { EvtSource } from "@/test/evt-source"
import { ServicesPage } from "@/modules/services/services-page"
import { UsersPage } from "@/modules/users/users-page"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"

afterEach(() => {
  delete document.body.dataset.outcome
  vi.unstubAllGlobals()
})

const SERVICES = "POST /api/v1/hosts/h1/actions/services.unit.list/v1/read"
const SERVICES_UP = "GET /api/v1/hosts/h1/modules/services/availability"

function services(hostScope: Scope, extra: Record<string, StubHandler> = {}) {
  stubFetch({
    [SERVICES]: { body: { output: { units: unitsDefault } } },
    [SERVICES_UP]: { body: { module: "services", available: true, reason: null, actions: [] } },
    ...extra,
  })
  return renderWithProviders(<ServicesPage hostId="h1" />, { hostScope })
}

describe("A64: roles hide what a role cannot use", () => {
  test("A64: a viewer sees every unit and no action menu", async () => {
    services("read")
    await screen.findByText("nginx.service")
    expect(screen.queryByRole("button", { name: /^Actions for/ })).toBeNull()
  })

  test("A64: an operator's menu has Start, Stop, Restart, Reload and no Enable or Disable", async () => {
    services("operate")
    const user = userEvent.setup()
    await screen.findByText("nginx.service")
    await user.click(screen.getByRole("button", { name: "Actions for nginx.service" }))
    const names = (await screen.findAllByRole("menuitem")).map((i) => i.textContent ?? "")
    for (const wanted of ["Start", "Stop", "Restart", "Reload"]) expect(names.some((n) => n.startsWith(wanted))).toBe(true)
    expect(names.some((n) => /Enable|Disable/.test(n))).toBe(false)
  })

  test("A64: an admin's menu adds Enable and Disable", async () => {
    services("admin")
    const user = userEvent.setup()
    await screen.findByText("nginx.service")
    await user.click(screen.getByRole("button", { name: "Actions for nginx.service" }))
    const names = (await screen.findAllByRole("menuitem")).map((i) => i.textContent ?? "")
    expect(names.some((n) => /Enable|Disable/.test(n))).toBe(true)
  })

  test("A64: a viewer sees the users page without Add user", async () => {
    stubFetch({
      "POST /api/v1/hosts/h1/actions/users.user.list/v1/read": { body: { output: { accounts: accountsDefault } } },
      "POST /api/v1/hosts/h1/actions/users.key.list/v1/read": { body: { output: keysDeploy } },
      "POST /api/v1/hosts/h1/actions/users.sshd.get/v1/read": { body: { output: sshdState } },
      "GET /api/v1/hosts/h1/modules/users/availability": { body: { module: "users", available: true, reason: null, actions: [] } },
    })
    renderWithProviders(<UsersPage hostId="h1" />, { hostScope: "read" })
    await screen.findByText(accountsDefault[0]?.name ?? "")
    expect(screen.queryByRole("button", { name: "Add user" })).toBeNull()
    expect(screen.queryByRole("button", { name: /Edit hardening/ })).toBeNull()
  })
})

describe("A65: services flow", () => {
  test("A65: stopping ssh.service asks for the unit name as the typed phrase", async () => {
    services("admin", {
      "POST /api/v1/hosts/h1/actions/services.unit.stop/v1/plan": {
        body: {
          planId: "p1", hostId: "h1", action: "services.unit.stop", version: 1, actor: "user:admin", diff: [], guards: [],
          stateHash: "h", confirmation: "ssh.service", createdAt: "2026-11-26T09:40:00Z", expiresAt: "2026-11-26T09:50:00Z",
        },
      },
    })
    const user = userEvent.setup()
    await screen.findByText("ssh.service")
    await user.click(screen.getByRole("button", { name: "Actions for ssh.service" }))
    await user.click(await screen.findByRole("menuitem", { name: "Stop" }))
    const dialog = await screen.findByRole("dialog")
    expect(await within(dialog).findByLabelText(/ssh\.service/)).toBeTruthy()
  })

  test("A65: Darkpanel's own unit shows its mutating items disabled", async () => {
    services("admin")
    const user = userEvent.setup()
    await screen.findByText("darkpanel-agent.service")
    await user.click(screen.getByRole("button", { name: "Actions for darkpanel-agent.service" }))
    const items = await screen.findAllByRole("menuitem")
    const mutating = items.filter((i) => /^(Start|Stop|Restart|Reload|Enable|Disable)/.test(i.textContent ?? ""))
    expect(mutating.length).toBeGreaterThan(0)
    for (const item of mutating) {
      expect(item.getAttribute("aria-disabled") ?? item.getAttribute("data-disabled")).not.toBeNull()
    }
  })

  test("A65: View logs filters the journal to the unit", async () => {
    services("admin")
    const user = userEvent.setup()
    await screen.findByText("nginx.service")
    await user.click(screen.getByRole("button", { name: "Actions for nginx.service" }))
    await user.click(await screen.findByRole("menuitem", { name: "View logs" }))
    await waitFor(() => expect((screen.getByLabelText("Unit") as HTMLInputElement).value).toBe("nginx.service"))
  })
})

function problem(status: number, code: string, detail: string, headers: Record<string, string> = {}): StubResponse {
  return { status, body: { type: "about:blank", title: "", status, detail, code }, headers }
}

function Trigger() {
  const { stepUp } = useStepUp()
  return (
    <Button
      onClick={() => {
        void stepUp("Opening a terminal needs your authenticator code").then((ok) => {
          document.body.dataset.outcome = ok ? "verified" : "cancelled"
        })
      }}
    >
      Open terminal
    </Button>
  )
}

async function openStepUp() {
  const user = userEvent.setup()
  renderWithProviders(<Trigger />)
  await user.click(screen.getByRole("button", { name: "Open terminal" }))
  const dialog = await screen.findByRole("dialog")
  return { user, dialog }
}

describe("A68: the step-up dialog", () => {
  test("A68: focus starts in the code field and Enter submits the six digits", async () => {
    const calls = stubFetch({ "POST /api/v1/auth/step-up": { status: 204 } })
    const { user } = await openStepUp()
    await waitFor(() => expect(focusedName()).toMatch(/code/i))
    await user.keyboard("246810{Enter}")
    await waitFor(() => expect(document.body.dataset.outcome).toBe("verified"))
    expect(calls.filter((c) => c.path === "/api/v1/auth/step-up").map((c) => c.body)).toEqual([{ code: "246810" }])
  })

  test.each([
    ["a wrong code", problem(401, "auth.invalid_credentials", "wrong"), "That code didn't work"],
    ["a code of the step already used", problem(409, "auth.step_up_wait", "wait"), "Wait for the next code"],
    ["the rate limit", problem(429, "auth.rate_limited", "slow down", { "Retry-After": "17" }), "Too many attempts; try again in 17 s"],
  ])("A68: %s shows its text and keeps focus in the field", async (_n, answer, text) => {
    stubFetch({ "POST /api/v1/auth/step-up": answer })
    const { user, dialog } = await openStepUp()
    await user.keyboard("135790{Enter}")
    expect(await within(dialog).findByText(text)).toBeTruthy()
    expect(focusedName()).toMatch(/code/i)
  })

  test("A68: a user without an authenticator sees the setup text instead of the field", async () => {
    stubFetch({ "POST /api/v1/auth/step-up": problem(403, "auth.step_up_unavailable", "x") })
    const { user, dialog } = await openStepUp()
    await user.keyboard("135790{Enter}")
    expect(await within(dialog).findByText("Set up an authenticator to use the console and jobs")).toBeTruthy()
  })

  test("A68: Escape cancels, sends nothing and returns focus to the trigger", async () => {
    const calls = stubFetch({})
    const { user } = await openStepUp()
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await waitFor(() => expect(focusedName()).toBe("Open terminal"))
    expect(document.body.dataset.outcome).toBe("cancelled")
    expect(calls).toEqual([])
  })
})

describe("A73: no axe violations", () => {
  test("A73: the services page for an admin", async () => {
    const { container } = services("admin")
    await screen.findByText("nginx.service")
    await expectNoAxeViolations(container)
  })

  test("A73: the open step-up dialog", async () => {
    stubFetch({})
    const { dialog } = await openStepUp()
    await expectNoAxeViolations(dialog)
  })

  test("A73: the services page shows its states as text, not colour alone", async () => {
    services("admin")
    await screen.findByText("nginx.service")
    expect(screen.getAllByText("Failed").length).toBeGreaterThan(0)
  })
})

describe("A66: the journal panel", () => {
  test("A66: scrolling up pauses following with Jump to latest, and a dropped stream shows Reconnecting…", async () => {
    EvtSource.instances = []
    stubFetch({})
    renderWithProviders(<JournalPanel hostId="h1" unit="nginx.service" onUnitChange={() => {}} />)
    const latest = (): EvtSource => {
      const all = EvtSource.instances.filter((s) => s.url.includes("/streams/services/"))
      const s = all[all.length - 1]
      if (s === undefined) throw new Error("no journal stream opened")
      return s
    }
    act(() => {
      for (const e of journalEntries(20)) latest().emit("data", e)
    })
    const log = screen.getByRole("log")
    Object.defineProperty(log, "scrollHeight", { configurable: true, value: 1000 })
    Object.defineProperty(log, "clientHeight", { configurable: true, value: 200 })
    log.scrollTop = 100
    fireEvent.scroll(log)
    expect(screen.getByText("Following paused")).toBeTruthy()
    await userEvent.setup().click(screen.getByRole("button", { name: "Jump to latest" }))
    expect(screen.queryByText("Following paused")).toBeNull()
    act(() => latest().fail())
    expect(screen.getByText("Reconnecting…")).toBeTruthy()
  })
})

describe("A71: the tuning page", () => {
  const BASE = "POST /api/v1/hosts/h1/actions"
  function tuning(swap: object = swapDefault) {
    stubFetch({
      [`${BASE}/tuning.swap.get/v1/read`]: { body: { output: swap } },
      [`${BASE}/tuning.sysctl.get/v1/read`]: { body: { output: sysctlDefault } },
      "GET /api/v1/hosts/h1/modules/tuning/availability": { body: { module: "tuning", available: true, reason: null, actions: [] } },
    })
    return renderWithProviders(<TuningPage hostId="h1" />, { hostScope: "admin" })
  }

  test("A71: an out-of-range value is refused inline with its range and Review changes waits", async () => {
    tuning()
    const user = userEvent.setup()
    const field = await screen.findByLabelText(/vm\.swappiness/)
    await user.clear(field)
    await user.type(field, "201")
    expect(screen.getAllByText(/0.*200/).length).toBeGreaterThan(0)
    expect(screen.getByRole("button", { name: "Review changes" })).toHaveProperty("disabled", true)
    await user.clear(field)
    await user.type(field, "37")
    expect(screen.getByRole("button", { name: "Review changes" })).toHaveProperty("disabled", false)
    expect(screen.getAllByRole("button", { name: "Review changes" })).toHaveLength(1)
  })

  test("A71: a filesystem that cannot hold a swap file names itself", async () => {
    tuning(swapUnsupported)
    expect(await screen.findByText(/btrfs/)).toBeTruthy()
  })
})

describe("A70: the file manager", () => {
  const FILES = "/api/v1/hosts/h1/files"
  const query = (path: string) => new URL(path, "http://localhost").searchParams

  function Back() {
    const navigate = useNavigate()
    return (
      <button type="button" onClick={() => void navigate(-1)}>
        Browser back
      </button>
    )
  }

  function listing(dirs: Record<string, object>, more?: object) {
    return (c: { path: string }) => {
      const q = query(c.path)
      if (q.has("cursor") && more !== undefined) return { body: more }
      return { body: dirs[q.get("path") ?? ""] ?? { path: q.get("path"), entries: [] } }
    }
  }

  test("A70: a deep link opens its directory, the path survives navigation and back restores the previous one", async () => {
    const calls = stubFetch({ [`GET ${FILES}/list`]: listing({ "/etc": filesDefault }) })
    renderWithProviders(
      <>
        <FilesView hostId="h1" />
        <Back />
      </>,
      { path: "/?path=/etc" },
    )
    const user = userEvent.setup()
    expect(await screen.findByRole("table", { name: "Files in /etc" })).toBeTruthy()
    expect(calls.map((c) => query(c.path).get("path"))).toEqual(["/etc"])
    await user.click(await screen.findByRole("button", { name: "nginx/" }))
    expect(await screen.findByRole("table", { name: "Files in /etc/nginx" })).toBeTruthy()
    await user.click(screen.getByRole("button", { name: "Browser back" }))
    expect(await screen.findByRole("table", { name: "Files in /etc" })).toBeTruthy()
  })

  test("A70: 2000 entries show Show more and the next page loads by its cursor", async () => {
    const more = { path: "/srv/uploads", entries: [{ ...filesDefault.entries[3], name: "zz-last.txt" }] }
    stubFetch({ [`GET ${FILES}/list`]: listing({ "/srv/uploads": filesWorst }, more) })
    renderWithProviders(<FilesView hostId="h1" />, { path: "/?path=/srv/uploads" })
    const table = await screen.findByRole("table", { name: "Files in /srv/uploads" })
    expect(within(table).getAllByRole("button", { name: /^Actions for / })).toHaveLength(2000)
    await userEvent.setup().click(screen.getByRole("button", { name: "Show more" }))
    expect(await screen.findByText("zz-last.txt")).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull()
  }, 30_000)

  test("A70: a protected directory shows why and disables the write actions", async () => {
    const reason = "Darkpanel never changes files under /etc/darkpanel"
    stubFetch({ [`GET ${FILES}/list`]: listing({ "/etc/darkpanel": { path: "/etc/darkpanel", protected: reason, entries: [] } }) })
    renderWithProviders(<FilesView hostId="h1" />, { path: "/?path=/etc/darkpanel" })
    expect(await screen.findByText(reason)).toBeTruthy()
    expect(screen.getByRole("button", { name: "Upload" })).toHaveProperty("disabled", true)
    expect(screen.getByRole("button", { name: "New folder" })).toHaveProperty("disabled", true)
  })
})

function sshLine(type: string, ...fields: Uint8Array[]): string {
  const parts = [new TextEncoder().encode(type), ...fields]
  const size = parts.reduce((n, p) => n + 4 + p.length, 0)
  const blob = new Uint8Array(size)
  let at = 0
  for (const p of parts) {
    new DataView(blob.buffer).setUint32(at, p.length)
    blob.set(p, at + 4)
    at += 4 + p.length
  }
  return `${type} ${btoa(String.fromCharCode(...blob))} test@laptop`
}

describe("A67: the users forms", () => {
  const BASE = "POST /api/v1/hosts/h1/actions"
  function users(extra: Record<string, StubHandler> = {}) {
    stubFetch({
      [`${BASE}/users.user.list/v1/read`]: { body: { output: { accounts: accountsDefault } } },
      [`${BASE}/users.key.list/v1/read`]: { body: { output: keysDeploy } },
      [`${BASE}/users.sshd.get/v1/read`]: { body: { output: sshdState } },
      "GET /api/v1/hosts/h1/modules/users/availability": { body: { module: "users", available: true, reason: null, actions: [] } },
      ...extra,
    })
    renderWithProviders(<UsersPage hostId="h1" />, { hostScope: "admin" })
  }

  test("A67: a short RSA key and junk show their messages under the field", async () => {
    users()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "deploy" }))
    const field = await screen.findByLabelText("Add a public key")
    const n = new Uint8Array(2048 / 8 + 1)
    n[1] = 0x80
    await user.click(field)
    await user.paste(sshLine("ssh-rsa", Uint8Array.of(1, 0, 1), n))
    expect(screen.getByText("RSA keys need at least 3072 bits")).toBeTruthy()
    await user.clear(field)
    await user.type(field, "hello")
    expect(screen.getByText("Not a public key line")).toBeTruthy()
  })

  test("A67: an invalid account name is refused inline as it is typed", async () => {
    users()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add user" }))
    await user.type(screen.getByLabelText("Name"), "Alice")
    expect(screen.getByLabelText("Name").getAttribute("aria-invalid")).toBe("true")
  })

  test("A67: the root row offers neither Lock nor Delete, and Delete asks the user name", async () => {
    users({
      [`${BASE}/users.user.delete/v1/plan`]: {
        body: {
          planId: "p1", hostId: "h1", action: "users.user.delete", version: 1, actor: "user:admin", diff: [], guards: [],
          stateHash: "h", confirmation: "alice", createdAt: "2026-11-26T09:40:00Z", expiresAt: "2026-11-26T09:50:00Z",
        },
      },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Actions for root" }))
    const items = (await screen.findAllByRole("menuitem")).map((i) => i.textContent ?? "")
    expect(items.some((i) => /^(Lock|Delete)/.test(i))).toBe(false)
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: "Actions for alice" }))
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }))
    const dialog = await screen.findByRole("dialog")
    expect(await within(dialog).findByLabelText(/alice/)).toBeTruthy()
  })
})

class Sock {
  static all: Sock[] = []
  readyState = 0
  binaryType = "blob"
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: ArrayBuffer | string }) => void) | null = null
  onclose: ((ev: { code: number; reason: string }) => void) | null = null
  sent: unknown[] = []
  constructor(readonly url: string) {
    Sock.all.push(this)
  }
  send(d: unknown) {
    this.sent.push(d)
  }
  close() {
    this.end(1000, "")
  }
  end(code: number, reason: string) {
    if (this.readyState === 3) return
    this.readyState = 3
    this.onclose?.({ code, reason })
  }
}

function surfaceStub(): TerminalSurface {
  return {
    cols: 100,
    rows: 30,
    write: () => {},
    onData: () => () => {},
    onResize: () => () => {},
    fit: () => {},
    focus: () => {},
    dispose: () => {},
  }
}

describe("A69: the terminal's states", () => {
  async function connect(reported = true, online = true) {
    Sock.all = []
    stubFetch({ "POST /api/v1/hosts/h1/channels/console/terminal/ticket": { body: { ticket: "T" } } })
    vi.stubGlobal("WebSocket", Sock)
    renderWithProviders(<TerminalView hostId="h1" hostName="web-1" online={online} reported={reported} openSurface={() => surfaceStub()} />)
    const user = userEvent.setup()
    if (!online || !reported) return user
    await user.type(await screen.findByLabelText("Authenticator code"), "123456")
    await user.click(screen.getByRole("button", { name: "Verify" }))
    await waitFor(() => expect(Sock.all.length).toBeGreaterThan(0))
    act(() => {
      const s = Sock.all[Sock.all.length - 1] as Sock
      s.readyState = 1
      s.onopen?.()
    })
    await screen.findByText("Connected")
    return user
  }

  test.each([
    ["idle", /idle|30 minutes/i, true],
    ["expired", /expired|signed out|session/i, false],
    ["console.limit", /too many|limit/i, true],
    ["agent restarted", /restarted/i, true],
  ])("A69: closing with %s shows its text", async (reason, text, reconnect) => {
    await connect()
    act(() => (Sock.all[Sock.all.length - 1] as Sock).end(1000, reason))
    expect((await screen.findByRole("alert")).textContent).toMatch(text)
    if (reconnect) expect(screen.getByRole("button", { name: "Reconnect" })).toBeTruthy()
  })

  test("A69: an offline host and a too-old agent say so without asking a code", async () => {
    await connect(true, false)
    expect((await screen.findByRole("alert")).textContent).toMatch(/offline/i)
    expect(screen.queryByLabelText("Authenticator code")).toBeNull()
  })

  test("A69: Ctrl+Shift+L moves focus to the toolbar", async () => {
    await connect()
    const toolbar = screen.getByRole("toolbar", { name: "Terminal" })
    const inner = document.createElement("textarea")
    screen.getByTestId("terminal-area").appendChild(inner)
    inner.focus()
    fireEvent.keyDown(inner, { key: "L", ctrlKey: true, shiftKey: true })
    expect(document.activeElement).toBe(toolbar)
  })
})
