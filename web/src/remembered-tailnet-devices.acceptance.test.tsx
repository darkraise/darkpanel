import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { afterEach, describe, expect, test } from "vitest"
import type { DirectoryUser, HostGroup, Session, SignInSettings, TailnetDevice } from "@/api/types"
import { formatTime } from "@/lib/format"
import { LoginPage } from "@/pages/login"
import { SignInSettingsPage } from "@/pages/sign-in-settings"
import { TailnetDevicesPage } from "@/pages/tailnet-devices"
import { UsersPage } from "@/pages/users"
import { RequireSession, SessionProvider } from "@/session/session"
import { expectNoAxeViolations } from "@/test/a11y"
import { makeChange, makePlan, renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"

// Acceptance tests A3 and A18 to A23, A31 and A32 of the remembered tailnet
// devices spec, written from its UI/UX contract.

const action = (name: string, op: string) => `POST /api/v1/actions/${name}/v1/${op}`
const THEMES = ["light", "dark"] as const

afterEach(() => {
  document.documentElement.classList.remove("dark")
})

const problem = (status: number, code: string): StubResponse => ({
  status,
  body: { type: "about:blank", title: code, status, detail: `server detail for ${code}`, code },
})

// ---- the login page --------------------------------------------------------------

const mia: Session = {
  user: { id: "u1", username: "mia", role: "member", allHosts: "", state: "active", createdAt: "2026-10-19T08:00:00Z" },
  csrfToken: "csrf-1",
  expiresAt: "2026-10-19T20:00:00Z",
  token: null,
}
const NOTE = "Continue as needs your authenticator code once. Finish account setup or sign in below."
const methods: StubResponse = { body: { oidc: null, tailscale: true } }
const matchOf = (remembered: boolean, codeReady: boolean, nodeName = "laptop.tail1234.ts.net", username = "mia"): StubResponse => ({
  body: { username, loginName: "mia@github", nodeName, remembered, codeReady },
})

function renderLogin(routes: Record<string, StubHandler>) {
  const calls = stubFetch({ "GET /api/v1/auth/session": problem(401, "auth.required"), "GET /api/v1/auth/methods": methods, ...routes })
  renderWithProviders(
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/" element={<RequireSession><p>Hosts content</p></RequireSession>} />
      </Routes>
    </SessionProvider>,
    { path: "/login" },
  )
  return calls
}

const continueAs = () => screen.findByRole("button", { name: /^Continue as mia/ })

describe("the login page", () => {
  test("A3: a pending user's match shows the note and no Continue as button", async () => {
    renderLogin({ "GET /api/v1/auth/tailscale": matchOf(false, false, "pend-pc.tail1234.ts.net", "pend") })
    expect(await screen.findByText(NOTE)).toBeTruthy()
    expect(screen.queryByRole("button", { name: /^Continue as/ })).toBeNull()
  })

  test.each([
    ["a name with labels", "laptop.tail1234.ts.net", "Enter your code once to remember laptop."],
    ["an empty name", "", "Enter your code once to remember this device."],
  ])("A18: the code step from a node not remembered shows the remember line for %s", async (_, nodeName, line) => {
    const calls = renderLogin({
      "GET /api/v1/auth/tailscale": matchOf(false, true, nodeName),
      "POST /api/v1/auth/tailscale": { body: { totp: true, remember: true } },
      "POST /api/v1/auth/totp": { body: mia },
    })
    const user = userEvent.setup()
    await user.click(await continueAs())
    expect(await screen.findByText(line)).toBeTruthy()
    await user.type(screen.getByLabelText("Authentication code"), "123456")
    await user.click(screen.getByRole("button", { name: "Log in" }))
    expect(await screen.findByText("Hosts content")).toBeTruthy()
    expect(calls.filter((c) => c.path === "/api/v1/auth/totp").map((c) => c.body)).toEqual([{ code: "123456" }])
  })

  test.each([
    ["a remembered node", true],
    ["a node not remembered", false],
  ])("A18: with requireTotp on the code step never shows the remember line, for %s", async (_, remembered) => {
    renderLogin({
      "GET /api/v1/auth/tailscale": matchOf(remembered, true),
      "POST /api/v1/auth/tailscale": { body: { totp: true, remember: false } },
    })
    await userEvent.setup().click(await continueAs())
    expect(await screen.findByLabelText("Authentication code")).toBeTruthy()
    expect(screen.queryByText(/^Enter your code once to remember/)).toBeNull()
  })

  test("A19: neither remembered nor code-ready shows the note, no button and a usable password form", async () => {
    const calls = renderLogin({
      "GET /api/v1/auth/tailscale": matchOf(false, false),
      "POST /api/v1/auth/login": { body: mia },
    })
    expect(await screen.findByText(NOTE)).toBeTruthy()
    expect(screen.queryByRole("button", { name: /^Continue as/ })).toBeNull()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText("Username"), "mia")
    await user.type(screen.getByLabelText("Password"), "correct horse")
    await user.type(screen.getByLabelText("Authentication code"), "123456")
    await user.click(screen.getByRole("button", { name: "Log in" }))
    expect(await screen.findByText("Hosts content")).toBeTruthy()
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/auth/tailscale")).toBe(false)
  })

  test("A19: a remembered node, or a code-ready user, still gets the button and no note", async () => {
    renderLogin({ "GET /api/v1/auth/tailscale": matchOf(true, false) })
    expect(await continueAs()).toBeTruthy()
    expect(screen.queryByText(NOTE)).toBeNull()
  })
})

// ---- the devices table: page and dialog -------------------------------------------

function device(stableId: string, nodeName: string, day = 1): TailnetDevice {
  const d = String(day).padStart(2, "0")
  return { stableId, nodeName, rememberedAt: `2026-10-${d}T09:00:00Z`, lastUsedAt: `2026-11-${d}T09:30:00Z` }
}

const LIST = action("core.tailnetdevice.list", "read")
const PLAN = action("core.tailnetdevice.forget", "plan")
const APPLY = action("core.tailnetdevice.forget", "apply")
const listed = (devices: TailnetDevice[]): StubResponse => ({ body: { output: { devices } } })

const forgetButton = (name: string) => screen.getByRole("button", { name: `Forget device ${name}` })

describe("the Tailscale devices page", () => {
  test("A20: lists the caller's devices with their dates and stable ids", async () => {
    const laptop = device("nL", "laptop.tail1234.ts.net", 2)
    const unnamed = device("nE", "", 1)
    stubFetch({ [LIST]: listed([laptop, unnamed]) })
    renderWithProviders(<TailnetDevicesPage />)
    expect(await screen.findByRole("heading", { level: 1, name: "Tailscale devices" })).toBeTruthy()
    expect(screen.getByText("Devices that may sign you in with Continue as without a code.")).toBeTruthy()
    const row = (await screen.findByText("laptop")).closest("tr") as HTMLElement
    for (const head of ["Device", "Remembered", "Last used"]) {
      expect(screen.getByRole("columnheader", { name: head })).toBeTruthy()
    }
    expect(within(row).getByText("laptop.tail1234.ts.net")).toBeTruthy()
    expect(within(row).getByText("nL")).toBeTruthy()
    expect(within(row).getByText(formatTime(laptop.rememberedAt))).toBeTruthy()
    expect(within(row).getByText(formatTime(laptop.lastUsedAt))).toBeTruthy()
    expect(within(row).getByRole("button", { name: "Forget device laptop" })).toBeTruthy()
    const blank = screen.getByText("Unnamed device").closest("tr") as HTMLElement
    expect(within(blank).getByText("nE")).toBeTruthy()
    expect(within(blank).getByRole("button", { name: "Forget device nE" })).toBeTruthy()
  })

  test("A20: the first load shows the spinner labelled Loading the devices", async () => {
    stubFetch({ [LIST]: () => new Promise<StubResponse>(() => undefined) })
    renderWithProviders(<TailnetDevicesPage />)
    expect(await screen.findByLabelText("Loading the devices")).toBeTruthy()
  })

  test("A20: no devices shows the empty state", async () => {
    stubFetch({ [LIST]: listed([]) })
    renderWithProviders(<TailnetDevicesPage />)
    expect(await screen.findByText("No remembered devices")).toBeTruthy()
    expect(screen.getByText("A device is remembered when you continue as yourself from it with your code.")).toBeTruthy()
  })

  test("A20: a failed read shows the error's message and keeps the header", async () => {
    stubFetch({ [LIST]: problem(500, "internal") })
    renderWithProviders(<TailnetDevicesPage />)
    const alert = await screen.findByRole("alert")
    expect(within(alert).getByText("server detail for internal")).toBeTruthy()
    expect(screen.getByRole("heading", { level: 1, name: "Tailscale devices" })).toBeTruthy()
  })

  test("A20: a 253-character name, an empty name and 40 devices all render whole", async () => {
    const long = [63, 63, 63, 61].map((n, i) => String.fromCharCode(97 + i).repeat(n)).join(".")
    expect(long).toHaveLength(253)
    const many = Array.from({ length: 40 }, (_, i) => device(`n${i}`, `host${i}.tail1234.ts.net`, 1 + (i % 28)))
    stubFetch({ [LIST]: listed([device("nLong", long, 3), device("nBlank", "", 2), ...many]) })
    renderWithProviders(<TailnetDevicesPage />)
    expect(await screen.findByText(long)).toBeTruthy()
    expect(screen.getByRole("button", { name: `Forget device ${"a".repeat(63)}` })).toBeTruthy()
    expect(screen.getByRole("button", { name: "Forget device nBlank" })).toBeTruthy()
    expect(screen.getAllByRole("button", { name: /^Forget device host\d+$/ })).toHaveLength(40)
  })
})

function forgetStubs(devices: { current: TailnetDevice[] }, forgetName: string, extra: Record<string, StubHandler> = {}) {
  return stubFetch({
    [action("core.user.directory", "read")]: { body: { output: { users: [dirUser({ username: "admin", role: "admin" }), dirUser({ username: "mia" })] } } },
    [action("core.group.list", "read")]: { body: { output: { groups: [] as HostGroup[] } } },
    [LIST]: () => listed(devices.current),
    [action("core.user.list-tailnet-devices", "read")]: () => listed(devices.current),
    [PLAN]: (call) => ({
      body: makePlan("core.tailnetdevice.forget", { planId: "p1", confirmation: (call.body as { input: { stableId: string } }).input.stableId }),
    }),
    [action("core.user.forget-tailnet-device", "plan")]: (call) => ({
      body: makePlan("core.user.forget-tailnet-device", { planId: "p1", confirmation: (call.body as { input: { stableId: string } }).input.stableId }),
    }),
    [APPLY]: () => applyForget(devices, forgetName),
    [action("core.user.forget-tailnet-device", "apply")]: () => applyForget(devices, forgetName),
    ...extra,
  })
}

// The stub's apply drops the device whose id the last plan carried.
let lastPlanned = ""
function applyForget(devices: { current: TailnetDevice[] }, name: string): StubResponse {
  devices.current = devices.current.filter((d) => d.stableId !== lastPlanned)
  return { body: makeChange(name, {}) }
}

describe("forgetting a device", () => {
  async function forgetOn(user: ReturnType<typeof userEvent.setup>, name: string, stableId: string, title: string, scope: HTMLElement | null = null) {
    lastPlanned = stableId
    const button = scope === null ? forgetButton(name) : within(scope).getByRole("button", { name: `Forget device ${name}` })
    await user.click(button)
    const change = await screen.findByRole("dialog", { name: title })
    await user.type(await within(change).findByLabelText(`Type ${stableId} to confirm`), stableId)
    await user.click(within(change).getByRole("button", { name: "Apply" }))
    await waitFor(() => expect(screen.queryByRole("dialog", { name: title })).toBeNull())
  }

  test("A21: on the page the phrase is the stable id, the row goes and focus follows the removed position", async () => {
    const devices = { current: [device("nA", "alpha.tail.ts.net", 3), device("nB", "bravo.tail.ts.net", 2), device("nC", "charlie.tail.ts.net", 1)] }
    const calls = forgetStubs(devices, "core.tailnetdevice.forget")
    renderWithProviders(<TailnetDevicesPage />)
    const user = userEvent.setup()
    await screen.findByText("bravo")

    await forgetOn(user, "bravo", "nB", "Forget device bravo")
    expect(calls.find((c) => c.path.endsWith("core.tailnetdevice.forget/v1/plan"))?.body).toEqual({ input: { stableId: "nB" } })
    await waitFor(() => expect(screen.queryByText("bravo")).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(forgetButton("charlie")))

    await forgetOn(user, "charlie", "nC", "Forget device charlie")
    await waitFor(() => expect(screen.queryByText("charlie")).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(forgetButton("alpha")))

    await forgetOn(user, "alpha", "nA", "Forget device alpha")
    expect(await screen.findByText("No remembered devices")).toBeTruthy()
    const h1 = screen.getByRole("heading", { level: 1, name: "Tailscale devices" })
    await waitFor(() => expect(document.activeElement).toBe(h1))
    expect(h1.getAttribute("tabindex")).toBe("-1")
  })

  test("A21: in the admin dialog the dialog stays open and focus follows the removed position", async () => {
    const devices = { current: [device("nA", "alpha.tail.ts.net", 3), device("nB", "bravo.tail.ts.net", 2)] }
    const calls = forgetStubs(devices, "core.user.forget-tailnet-device")
    renderWithProviders(<UsersPage />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Actions for mia" }))
    await user.click(await screen.findByRole("menuitem", { name: "Tailscale devices" }))
    const dialog = await screen.findByRole("dialog", { name: "Tailscale devices of mia" })
    await within(dialog).findByText("alpha")

    await forgetOn(user, "alpha", "nA", "Forget device alpha of mia", dialog)
    expect(calls.find((c) => c.path.endsWith("core.user.forget-tailnet-device/v1/plan"))?.body).toEqual({ input: { username: "mia", stableId: "nA" } })
    expect(screen.getByRole("dialog", { name: "Tailscale devices of mia" })).toBeTruthy()
    await waitFor(() => expect(screen.queryByText("alpha")).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "Forget device bravo" })))

    await forgetOn(user, "bravo", "nB", "Forget device bravo of mia", dialog)
    expect(await within(dialog).findByText("mia has no remembered devices.")).toBeTruthy()
    const title = within(dialog).getByRole("heading", { name: "Tailscale devices of mia" })
    await waitFor(() => expect(document.activeElement).toBe(title))
    expect(title.getAttribute("tabindex")).toBe("-1")
  })
})

// ---- the Users page -----------------------------------------------------------------

function dirUser(over: Partial<DirectoryUser>): DirectoryUser {
  return {
    id: `u-${over.username ?? "x"}`, username: "x", role: "member", state: "active", createdAt: "2026-11-20T08:00:00Z", disabled: false,
    grants: [], email: "", tailnetLogin: "", signIn: ["password"], inviteExpiresAt: null, ...over,
  }
}

function stubUsers(users: DirectoryUser[], devices: TailnetDevice[] = []) {
  return stubFetch({
    [action("core.user.directory", "read")]: { body: { output: { users } } },
    [action("core.group.list", "read")]: { body: { output: { groups: [] as HostGroup[] } } },
    [action("core.user.list-tailnet-devices", "read")]: listed(devices),
  })
}

async function menuItems(name: string): Promise<string[]> {
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }))
  const items = (await screen.findAllByRole("menuitem")).map((i) => i.textContent ?? "")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("menu")).toBeNull())
  return items
}

describe("the Users page", () => {
  test("A22: the row menu opens the devices dialog; Escape closes it and returns focus to the trigger", async () => {
    const laptop = device("nL", "laptop.tail1234.ts.net", 2)
    const longName = "u".repeat(64)
    const calls = stubUsers([dirUser({ username: "admin", role: "admin" }), dirUser({ username: "mia" }), dirUser({ username: longName })], [laptop])
    renderWithProviders(<UsersPage />)
    const user = userEvent.setup()
    const trigger = await screen.findByRole("button", { name: "Actions for mia" })
    await user.click(trigger)
    await user.click(await screen.findByRole("menuitem", { name: "Tailscale devices" }))
    const dialog = await screen.findByRole("dialog", { name: "Tailscale devices of mia" })
    expect(await within(dialog).findByText("laptop.tail1234.ts.net")).toBeTruthy()
    expect(within(dialog).getByRole("button", { name: "Forget device laptop" })).toBeTruthy()
    expect(calls.find((c) => c.path.endsWith("core.user.list-tailnet-devices/v1/read"))?.body).toEqual({ input: { username: "mia" } })
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await waitFor(() => expect(document.activeElement).toBe(trigger))

    await user.click(screen.getByRole("button", { name: `Actions for ${longName}` }))
    await user.click(await screen.findByRole("menuitem", { name: "Tailscale devices" }))
    expect(await screen.findByRole("dialog", { name: `Tailscale devices of ${longName}` })).toBeTruthy()
  })

  test("A31: New enrollment link is offered for an active user without a password and not for one with it", async () => {
    stubUsers([
      dirUser({ username: "admin", role: "admin" }),
      dirUser({ username: "oidcuser", signIn: ["oidc"] }),
      dirUser({ username: "nosignin", signIn: [] }),
      dirUser({ username: "pwuser", signIn: ["password", "oidc"] }),
    ])
    renderWithProviders(<UsersPage />)
    expect(await menuItems("oidcuser")).toContain("New enrollment link")
    expect(await menuItems("nosignin")).toContain("New enrollment link")
    expect(await menuItems("pwuser")).not.toContain("New enrollment link")
  })
})

// ---- Settings -> Sign-in -------------------------------------------------------------

describe("Settings sign-in", () => {
  test("A32: the Tailscale card shows the label Require the code every time and its hint", async () => {
    const settings: SignInSettings = { oidc: null, tailscale: { enabled: true, requireTotp: false } }
    stubFetch({
      [action("auth.settings.get", "read")]: { body: { output: settings } },
      "GET /api/v1/controller/listeners": { body: { tailscaleBound: true, message: "" } },
    })
    renderWithProviders(<SignInSettingsPage />)
    const card = await screen.findByRole("region", { name: "Tailscale identity" })
    const toggle = within(card).getByRole("switch", { name: "Require the code every time" })
    expect(within(card).getByText("When off, each device asks for the code once and is then remembered.")).toBeTruthy()
    const hint = document.getElementById(toggle.getAttribute("aria-describedby") ?? "")
    expect(hint?.textContent).toBe("When off, each device asks for the code once and is then remembered.")
    expect(within(card).queryByRole("switch", { name: "Require Darkpanel code too" })).toBeNull()
  })
})

// ---- accessibility --------------------------------------------------------------------

describe("accessibility", () => {
  test.each(THEMES)("A23: the devices page has no axe violations in %s", async (theme) => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    stubFetch({ [LIST]: listed([device("nL", "laptop.tail1234.ts.net", 2), device("nE", "", 1)]) })
    const { container } = renderWithProviders(<TailnetDevicesPage />)
    expect(await screen.findByText("Unnamed device")).toBeTruthy()
    await expectNoAxeViolations(container)
  })

  test.each(THEMES)("A23: the empty and error devices pages have no axe violations in %s", async (theme) => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    for (const [answer, ready] of [
      [listed([]), "No remembered devices"],
      [problem(500, "internal"), "server detail for internal"],
    ] as const) {
      stubFetch({ [LIST]: answer })
      const { container, unmount } = renderWithProviders(<TailnetDevicesPage />)
      expect(await screen.findByText(ready)).toBeTruthy()
      await expectNoAxeViolations(container)
      unmount()
    }
  })

  test.each(THEMES)("A23: the devices dialog has no axe violations in %s", async (theme) => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    stubUsers([dirUser({ username: "admin", role: "admin" }), dirUser({ username: "mia" })], [device("nL", "laptop.tail1234.ts.net", 2), device("nE", "", 1)])
    renderWithProviders(<UsersPage />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Actions for mia" }))
    await user.click(await screen.findByRole("menuitem", { name: "Tailscale devices" }))
    const dialog = await screen.findByRole("dialog", { name: "Tailscale devices of mia" })
    await within(dialog).findByText("Unnamed device")
    await expectNoAxeViolations(dialog)
  })

  test.each(THEMES)("A23: the login states of UX-6 have no axe violations in %s", async (theme) => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    const states: [string, StubResponse, StubResponse | null][] = [
      ["remembered", matchOf(true, true), null],
      ["code-ready", matchOf(false, true), { body: { totp: true, remember: true } }],
      ["neither", matchOf(false, false), null],
    ]
    for (const [name, match, post] of states) {
      stubFetch({
        "GET /api/v1/auth/session": problem(401, "auth.required"),
        "GET /api/v1/auth/methods": methods,
        "GET /api/v1/auth/tailscale": match,
        ...(post === null ? {} : { "POST /api/v1/auth/tailscale": post }),
      })
      const { container, unmount } = renderWithProviders(
        <SessionProvider>
          <LoginPage />
        </SessionProvider>,
        { path: "/login" },
      )
      if (name === "neither") expect(await screen.findByText(NOTE)).toBeTruthy()
      else {
        const button = await continueAs()
        if (post !== null) {
          await userEvent.setup().click(button)
          expect(await screen.findByText("Enter your code once to remember laptop.")).toBeTruthy()
        }
      }
      await expectNoAxeViolations(container)
      unmount()
    }
  })
})
