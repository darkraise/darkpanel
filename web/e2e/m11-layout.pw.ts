import { createRequire } from "node:module"
import { appendFileSync, mkdirSync } from "node:fs"
import { expect, test, type Page, type Route } from "@playwright/test"
import type { Host } from "../src/api/types"
import * as m11 from "../src/modules/m11-fixtures"
import { HOST_ID, mockApi } from "./fixtures"

// The M11 host administration screens (UI-1 to UI-8, UX-1 to UX-12, OP-1 to
// OP-5) at every viewport and theme of the profile, on worst-case data, with
// every state of the matrix forced, plus the screenshots for the proof report.
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
type Theme = (typeof THEMES)[number]
type Viewport = (typeof VIEWPORTS)[number]
const EVIDENCE = "../.dforge/ui-ux/m11-host-admin"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`
const tab = (name: string, query = "") => url(`/hosts/${HOST_ID}/${name}${query}`)
const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const CODE = "123456"

test.describe.configure({ timeout: 300_000 })

// What the run saw, written beside the screenshots for the report.
const tally = { audits: 0, axeRuns: 0, axeFindings: [] as string[], overflowFindings: [] as string[], consoleErrors: [] as string[], forcedFailures: 0, screenshots: 0 }

// A failed test restarts the worker, so each worker appends its own line.
test.afterAll(() => {
  mkdirSync(EVIDENCE, { recursive: true })
  appendFileSync(`${EVIDENCE}/summary.jsonl`, `${JSON.stringify(tally)}\n`)
})

const problem = (status: number, code: string, detail: string, headers: Record<string, string> = {}) => ({
  status,
  headers,
  contentType: "application/json",
  body: JSON.stringify({ type: "about:blank", title: detail, status, detail, code }),
})
const ok = (output: unknown) => ({ status: 200, contentType: "application/json", body: JSON.stringify({ output }) })
const readRoute = (name: string) => new RegExp(`/actions/${name.replace(/\./g, "\\.")}/v1/read$`)
const planRoute = (name: string) => new RegExp(`/actions/${name.replace(/\./g, "\\.")}/v1/plan$`)
const applyRoute = (name: string) => new RegExp(`/actions/${name.replace(/\./g, "\\.")}/v1/apply$`)
const hang = () => {}
const busy = (route: Route) => route.fulfill(problem(409, "host.busy", "The host is busy; try again"))

// The browser side of the journal follow: an EventSource of the journal
// stream the test drives, and a WebSocket whose open can be held, so the
// connecting state of the terminal can be seen.
const BROWSER_SEAMS = () => {
  const w = window as unknown as Record<string, unknown>
  const Real = window.EventSource
  const sources: (EventTarget & { url: string; closed: boolean; at: number })[] = []
  class Fake extends EventTarget {
    url: string
    closed = false
    at = Date.now()
    constructor(u: string) {
      super()
      this.url = u
      sources.push(this)
    }
    close() {
      this.closed = true
    }
  }
  const make = function (this: unknown, u: string | URL, init?: EventSourceInit) {
    return /journal-follow/.test(String(u)) ? (new Fake(String(u)) as unknown as EventSource) : new Real(u, init)
  }
  window.EventSource = make as unknown as typeof EventSource
  const live = () => [...sources].reverse().find((s) => !s.closed)
  const message = (type: string, data: unknown) => live()?.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }))
  w.__journal = {
    sources,
    open: () => live()?.dispatchEvent(new Event("open")),
    emit: (entries: unknown[]) => entries.forEach((e) => message("data", e)),
    drop: () => live()?.dispatchEvent(new Event("error")),
    end: () => message("end", { reason: "done" }),
  }
  const RealWS = window.WebSocket
  w.__wsHold = false
  w.__wsRelease = [] as (() => void)[]
  const handlers = new WeakMap<object, ((this: WebSocket, ev: Event) => unknown) | null>()
  class HeldWS extends RealWS {
    constructor(u: string | URL, p?: string | string[]) {
      super(u, p)
      handlers.set(this, null)
      super.addEventListener("open", (ev) => {
        const run = () => handlers.get(this)?.call(this, ev)
        if (w.__wsHold === true) (w.__wsRelease as (() => void)[]).push(run)
        else run()
      })
    }
    override set onopen(fn: ((this: WebSocket, ev: Event) => unknown) | null) {
      handlers.set(this, fn)
    }
    override get onopen() {
      return handlers.get(this) ?? null
    }
  }
  window.WebSocket = HeldWS as unknown as typeof WebSocket
}

interface Entry {
  time: number
  priority: number | null
  unit: string
  pid: number
  message: string
  truncated: boolean
}

const journal = {
  open: (page: Page) => page.evaluate(() => (window as unknown as { __journal: { open: () => void } }).__journal.open()),
  emit: (page: Page, entries: Entry[]) => page.evaluate((e) => (window as unknown as { __journal: { emit: (x: unknown[]) => void } }).__journal.emit(e), entries),
  drop: (page: Page) => page.evaluate(() => (window as unknown as { __journal: { drop: () => void } }).__journal.drop()),
  sources: (page: Page) => page.evaluate(() => (window as unknown as { __journal: { sources: { at: number; closed: boolean }[] } }).__journal.sources.map((s) => s.at)),
}

interface Boot {
  theme: Theme
  vp: Viewport
  m11?: true | "operate" | "read"
  worst?: boolean
  console?: boolean
  jobs?: boolean
  editHost?: (h: Host) => Host
}

async function boot(page: Page, o: Boot) {
  await page.setViewportSize(o.vp)
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), o.theme)
  await page.addInitScript(BROWSER_SEAMS)
  const fixtures = await mockApi(page, { signedIn: true, m11: o.m11 ?? true, worstCase: o.worst ?? false, console: o.console, jobs: o.jobs, editHost: o.editHost })
  await page.route(/\/api\/v1\/events$/, (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 200\n\n" }))
  page.on("console", (m) => {
    if (m.type() !== "error") return
    // Vite's own hot-reload socket cannot open on the port 0 the harness serves from.
    if (m.text().startsWith("Failed to load resource")) tally.forcedFailures++
    else if (/\/\?token=|Failed to send error to Vite server/.test(m.text())) return
    else tally.consoleErrors.push(`${page.url()} ${m.text()}`)
  })
  page.on("pageerror", (e) => tally.consoleErrors.push(`${page.url()} ${e.message}`))
  return fixtures
}

// Wait for every finite animation and transition to end, so a screenshot
// shows the settled state.
async function settle(page: Page) {
  await page.waitForFunction(() => document.getAnimations().every((a) => a.playState !== "running" || a.effect?.getComputedTiming().iterations === Infinity))
}

// The elements that run past the viewport's right edge outside a box the
// user can scroll sideways.
async function escaping(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = []
    const w = window.innerWidth
    for (const el of document.querySelectorAll<HTMLElement>("body *")) {
      const r = el.getBoundingClientRect()
      if (r.width === 0 || r.height === 0 || r.right <= w + 1 || r.width <= 1 || r.height <= 1) continue
      if (el.closest("[data-sonner-toaster], .sr-only")) continue
      let p = el.parentElement
      let scrolls = false
      while (p !== null && p !== document.body) {
        const o = getComputedStyle(p).overflowX
        if (o === "auto" || o === "scroll") {
          scrolls = true
          break
        }
        p = p.parentElement
      }
      if (!scrolls) out.push(`${el.tagName.toLowerCase()}${el.id !== "" ? `#${el.id}` : ""}: ${(el.textContent ?? "").trim().slice(0, 40)} (right ${Math.round(r.right)} > ${w})`)
    }
    return out.slice(0, 6)
  })
}

// Text in a cell or card that runs past its own box (a clipped value), outside
// a box the user can scroll sideways.
async function clipped(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const out: string[] = []
    for (const box of document.querySelectorAll<HTMLElement>("main td, main [role='group'], main li")) {
      if (box.tagName === "TD" && getComputedStyle(box).display === "table-cell") continue
      let up = box.parentElement
      let scrolls = false
      while (up !== null && up !== document.body) {
        const o = getComputedStyle(up).overflowX
        if (o === "auto" || o === "scroll") scrolls = true
        up = up.parentElement
      }
      if (scrolls) continue
      const range = document.createRange()
      range.selectNodeContents(box)
      const inner = range.getBoundingClientRect()
      const outer = box.getBoundingClientRect()
      if (outer.width === 0 || inner.width === 0) continue
      if (inner.left < outer.left - 1 || inner.right > outer.right + 1) out.push(`${box.tagName}: ${(box.textContent ?? "").slice(0, 40)}`)
    }
    return out.slice(0, 6)
  })
}

// WCAG 2.1 A and AA through axe, colour contrast included.
async function axeScan(page: Page): Promise<string[]> {
  if (!(await page.evaluate(() => "axe" in window))) await page.addScriptTag({ path: axePath })
  return page.evaluate(async () => {
    type Result = { violations: { id: string; impact: string; nodes: { target: unknown[]; html: string; any: { message: string }[] }[] }[] }
    const axe = (window as unknown as { axe: { run: (c: Document, o: object) => Promise<Result> } }).axe
    const result = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] })
    return result.violations.flatMap((v) => v.nodes.map((n) => `${v.id} (${v.impact}): ${n.target.join(" ")} ${n.html.slice(0, 90)} ${n.any[0]?.message ?? ""}`))
  })
}

// audit runs the automated checks on the settled view.
async function audit(page: Page, label: string) {
  tally.audits++
  const sideways = await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
  if (!sideways) tally.overflowFindings.push(`${label}: the page scrolls sideways`)
  expect.soft(sideways, `${label}: no sideways page scroll`).toBe(true)
  const past = await escaping(page)
  tally.overflowFindings.push(...past.map((p) => `${label}: ${p}`))
  expect.soft(past, `${label}: nothing runs past the viewport outside a scroll box`).toEqual([])
  const cut = await clipped(page)
  tally.overflowFindings.push(...cut.map((p) => `${label}: clipped ${p}`))
  expect.soft(cut, `${label}: text stays inside its cell`).toEqual([])
  tally.axeRuns++
  const found = await axeScan(page)
  tally.axeFindings.push(...found.map((f) => `${label}: ${f}`))
  expect.soft(found, `${label}: axe finds no WCAG 2.1 AA violation`).toEqual([])
}

// snap audits and screenshots the settled view as <name>-<width>-<theme>[-<state>].png.
// The shell scrolls inside its own container, so a page with no dialog open
// is shot in a window as tall as its content (at most 5000 px), at the same
// width; a worst-case list is shot at the viewport's own height.
async function snap(page: Page, o: { theme: Theme; vp: Viewport }, name: string, state = "", tallPage = true) {
  await settle(page)
  const file = `${name}-${o.vp.width}-${o.theme}${state === "" ? "" : `-${state}`}`
  await audit(page, file)
  const tall = tallPage && (await page.locator("[role=dialog]").count()) === 0 && name !== "console"
  const need = tall
    ? await page.evaluate(() => {
        let el = document.querySelector("main")
        while (el !== null && !/auto|scroll/.test(getComputedStyle(el).overflowY)) el = el.parentElement
        return el === null ? 0 : el.scrollHeight + (window.innerHeight - el.clientHeight)
      })
    : 0
  const grown = need > o.vp.height
  if (grown) {
    await page.setViewportSize({ width: o.vp.width, height: Math.min(need, 5000) })
    await settle(page)
  }
  await page.screenshot({ path: `${EVIDENCE}/${file}.png` })
  if (grown) await page.setViewportSize(o.vp)
  tally.screenshots++
}

const dialogOf = (page: Page, name: string | RegExp) => page.getByRole("dialog", { name })

async function enterCode(page: Page, code = CODE) {
  const field = page.getByRole("dialog", { name: "Enter your authenticator code" }).getByLabel("Authenticator code")
  await expect(field).toBeFocused()
  await page.keyboard.type(code)
  await page.keyboard.press("Enter")
}

// forced loads the page with one read held back (loading), failed (error) or
// emptied, and calls check on the state it shows.
async function forced(page: Page, read: string, mode: "loading" | "error", go: () => Promise<unknown>, check: () => Promise<void>) {
  const route = readRoute(read)
  const handler = mode === "loading" ? hang : busy
  await page.route(route, handler)
  await go()
  await check()
  await page.unroute(route, handler)
}

function ed25519Line(comment: string): string {
  const type = Buffer.from("ssh-ed25519")
  const blob = Buffer.alloc(4 + type.length + 4 + 32, 7)
  blob.writeUInt32BE(type.length, 0)
  type.copy(blob, 4)
  blob.writeUInt32BE(32, 4 + type.length)
  return `ssh-ed25519 ${blob.toString("base64")} ${comment}`
}

function rsaLine(bits: number): string {
  const field = (b: Buffer) => {
    const size = Buffer.alloc(4)
    size.writeUInt32BE(b.length)
    return Buffer.concat([size, b])
  }
  const modulus = Buffer.alloc(bits / 8 + 1, 0x55)
  modulus[0] = 0
  modulus[1] = 0xff
  return `ssh-rsa ${Buffer.concat([field(Buffer.from("ssh-rsa")), field(Buffer.from([1, 0, 1])), field(modulus)]).toString("base64")} tester@laptop`
}

const NOW = Date.now()

// withoutCommand is a job or cron row as the agent answers a viewer.
function withoutCommand<T extends { command?: string }>(row: T): Omit<T, "command"> {
  const copy = { ...row }
  delete copy.command
  return copy
}

const plan = (over: Record<string, unknown>) => ({
  planId: "plan-fixture",
  hostId: HOST_ID,
  action: "x",
  version: 1,
  inputHash: "sha256:fixture",
  diff: [],
  guards: [],
  expiresAt: "2099-01-01T00:00:00Z",
  confirmation: null,
  ...over,
})

const pendingChange = (action: string, deadlineSeconds: number) => ({
  id: "01JE000000000000000000C0F1",
  hostId: HOST_ID,
  action,
  version: 1,
  class: "revertible",
  state: "pending",
  actor: "user:admin",
  createdAt: "2026-11-27T09:00:00Z",
  updatedAt: "2026-11-27T09:00:00Z",
  revertDeadline: new Date(NOW + deadlineSeconds * 1000).toISOString(),
  output: null,
  error: null,
  compensatedBy: null,
  parentChangeId: null,
  owner: null,
  repairedBy: null,
  repairOutcome: null,
  override: null,
})

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const here = { theme, vp }
    const label = `${vp.width} in ${theme}`

    // UI-1, UI-2, UX-2: the Services tab on 400 units with 120-character names.
    test(`services worst case at ${label}`, async ({ page }) => {
      await boot(page, { ...here, worst: true })
      await page.goto(tab("services"))
      await expect(page.getByRole("heading", { name: "Journal", exact: true })).toBeVisible()
      const rows = page.locator("main table").first().locator("tbody tr")
      await expect(rows).toHaveCount(400)
      const first = rows.first().locator("span[title]").first()
      const name = await first.getAttribute("title")
      expect(name?.length, "the tooltip holds the whole 120-character name").toBe(120)
      expect(await first.evaluate((el) => getComputedStyle(el).textOverflow === "ellipsis" && el.scrollWidth > el.clientWidth), "the long name is truncated").toBe(true)
      await expect(rows.getByText("Failed", { exact: true }).first()).toBeVisible()
      await journal.open(page)
      await journal.emit(page, m11.journalEntries(2100))
      const log = page.getByRole("log", { name: "Journal entries" })
      await expect(log.locator("li")).toHaveCount(2000)
      expect(await log.evaluate((el) => getComputedStyle(el).fontFamily), "the journal is monospace").toContain("JetBrains Mono")
      await expect(log.getByText("… (truncated)").first()).toBeVisible()
      await snap(page, here, "services", "", false)
    })

    // UI-1, UX-2: states, the unit menu, ChangeDialog, the protected phrase and View logs.
    test(`services states at ${label}`, async ({ page }) => {
      await boot(page, here)
      await forced(page, "services.unit.list", "loading", () => page.goto(tab("services")), async () => {
        await expect(page.getByRole("status", { name: "Loading the services" })).toBeVisible()
        await snap(page, here, "services", "loading")
      })
      await forced(page, "services.unit.list", "error", () => page.reload(), async () => {
        await expect(page.getByText("Couldn't load services", { exact: true })).toBeVisible()
        await snap(page, here, "services", "error")
      })
      await page.getByRole("button", { name: "Retry" }).click()
      const trigger = page.getByRole("button", { name: "Actions for nginx.service", exact: true })
      await expect(trigger).toBeVisible()
      await snap(page, here, "services", "default")

      const search = page.getByLabel("Search units")
      await search.fill("zzz-nothing")
      await expect(page.getByText("No units match 'zzz-nothing'", { exact: true })).toBeVisible()
      await snap(page, here, "services", "filtered-empty")
      await page.getByRole("button", { name: "Clear filter" }).click()
      await expect(trigger).toBeVisible()
      await page.getByLabel("State").selectOption("failed")
      const shown = page.locator("main table").first().locator("tbody tr")
      await expect(shown).toHaveCount(2)
      await expect(shown.getByText("Failed", { exact: true })).toHaveCount(2)
      await page.getByLabel("State").selectOption("all")

      // The keyboard walk: the search field, the state filter, then the first row's menu.
      await search.focus()
      await page.keyboard.press("Tab")
      await expect(page.getByLabel("State")).toBeFocused()
      await page.keyboard.press("Tab")
      await expect(page.getByRole("button", { name: /^Actions for / }).first()).toBeFocused()

      await trigger.focus()
      await page.keyboard.press("Enter")
      for (const item of ["Start", "Stop", "Restart", "Reload", "Enable", "Disable", "View logs"]) await expect(page.getByRole("menuitem", { name: item, exact: true })).toBeVisible()
      await snap(page, here, "services", "menu")
      await page.keyboard.press("Escape")
      await expect(trigger).toBeFocused()

      await page.getByRole("button", { name: "Actions for darkpanel-agent.service", exact: true }).click()
      await expect(page.getByRole("menuitem", { name: /^Restart/ })).toHaveAttribute("aria-disabled", "true")
      await expect(page.getByText("Darkpanel manages its own units").first()).toBeVisible()
      await snap(page, here, "services", "own-unit-menu")
      await page.keyboard.press("Escape")

      await trigger.click()
      await page.getByRole("menuitem", { name: "Restart", exact: true }).click()
      const restart = dialogOf(page, "Restart nginx.service")
      await expect(restart.getByRole("button", { name: "Apply", exact: true })).toBeEnabled()
      await snap(page, here, "services", "restart-dialog")
      await page.keyboard.press("Escape")
      await expect(restart).toBeHidden()

      await page.getByRole("button", { name: "Actions for ssh.service", exact: true }).click()
      await page.getByRole("menuitem", { name: "Restart", exact: true }).click()
      const guarded = dialogOf(page, "Restart ssh.service")
      const phrase = guarded.getByLabel(/to confirm$/)
      await expect(phrase).toBeVisible()
      await expect(guarded.getByRole("button", { name: "Apply", exact: true })).toBeDisabled()
      await phrase.fill("ssh.service")
      await expect(guarded.getByRole("button", { name: "Apply", exact: true })).toBeEnabled()
      await snap(page, here, "services", "protected-dialog")
      await page.keyboard.press("Escape")

      await trigger.click()
      await page.getByRole("menuitem", { name: "View logs", exact: true }).click()
      await expect(page.getByLabel("Unit", { exact: true })).toHaveValue("nginx.service")
      await snap(page, here, "services", "view-logs")
    })

    // UI-2, UX-3: following, paused, reconnecting with a growing delay, empty.
    test(`journal states at ${label}`, async ({ page }) => {
      await boot(page, here)
      await page.goto(tab("services"))
      await expect(page.getByRole("heading", { name: "Journal", exact: true })).toBeVisible()
      await journal.open(page)
      await journal.emit(page, m11.journalEntries(120))
      const log = page.getByRole("log", { name: "Journal entries" })
      await expect(log.locator("li")).toHaveCount(120)
      await expect(page.getByText("Following paused")).toHaveCount(0)
      await expect(log.getByText("info").first()).toBeVisible()
      await snap(page, here, "journal", "following")
      await log.evaluate((el) => (el.scrollTop = 0))
      await expect(page.getByText("Following paused")).toBeVisible()
      await snap(page, here, "journal", "paused")
      await page.getByRole("button", { name: "Jump to latest" }).click()
      await expect(page.getByText("Following paused")).toHaveCount(0)

      await journal.drop(page)
      await expect(page.getByText("Reconnecting…", { exact: true })).toBeVisible()
      await snap(page, here, "journal", "reconnecting")
      await expect.poll(async () => (await journal.sources(page)).length, { timeout: 5000 }).toBe(2)
      await journal.drop(page)
      await expect.poll(async () => (await journal.sources(page)).length, { timeout: 8000 }).toBe(3)
      const at = await journal.sources(page)
      const first = (at[1] ?? 0) - (at[0] ?? 0)
      const second = (at[2] ?? 0) - (at[1] ?? 0)
      expect(second, `the second reconnect waits longer than the first (${first} ms, then ${second} ms)`).toBeGreaterThan(first)

      await page.getByLabel("Follow").click()
      await expect(page.getByRole("log", { name: "Journal entries" })).toBeVisible()
      await snap(page, here, "journal", "follow-off")
      await page.getByLabel("Follow").click()
      await journal.open(page)
      await expect(page.getByText("No journal entries match", { exact: true })).toBeVisible()
      await snap(page, here, "journal", "empty")
    })

    // UI-3, UX-4: the Users and SSH tab on 200 accounts.
    test(`users worst case at ${label}`, async ({ page }) => {
      await boot(page, { ...here, worst: true })
      await page.goto(tab("users"))
      await expect(page.getByRole("heading", { name: "SSH daemon", exact: true })).toBeVisible()
      await expect(page.getByRole("table", { name: "Login accounts" }).locator("tbody tr")).toHaveCount(200)
      await expect(page.getByText("Sudo", { exact: true }).first()).toBeVisible()
      await expect(page.getByText("Locked", { exact: true }).first()).toBeVisible()
      await page.getByRole("button", { name: "Actions for root", exact: true }).click()
      await expect(page.getByRole("menuitem", { name: "Keys", exact: true })).toBeVisible()
      for (const gone of ["Lock", "Delete", "Add to sudo", "Remove from sudo"]) await expect(page.getByRole("menuitem", { name: gone, exact: true }), `root has no ${gone}`).toHaveCount(0)
      await page.keyboard.press("Escape")
      await snap(page, here, "users", "", false)
    })

    // UI-3, UX-4, UX-5: states, the add user form, the key sheet, delete, hardening and its guard.
    test(`users states at ${label}`, async ({ page }) => {
      await boot(page, here)
      await forced(page, "users.user.list", "loading", () => page.goto(tab("users")), async () => {
        await expect(page.getByRole("status", { name: "Loading the users" })).toBeVisible()
        await snap(page, here, "users", "loading")
      })
      await forced(page, "users.user.list", "error", () => page.reload(), async () => {
        await expect(page.getByText("Couldn't load users", { exact: true })).toBeVisible()
        await snap(page, here, "users", "error")
      })
      await page.getByRole("button", { name: "Retry" }).first().click()
      await expect(page.getByRole("button", { name: "deploy", exact: true })).toBeVisible()
      await expect(page.getByRole("heading", { name: "SSH daemon", exact: true })).toBeVisible()
      await expect(page.getByText("Keys only", { exact: true })).toBeVisible()
      await snap(page, here, "users", "default")

      const add = page.getByRole("button", { name: "Add user", exact: true })
      await add.click()
      const form = dialogOf(page, "Add a user")
      await expect(form.getByLabel("Name")).toBeFocused()
      await form.getByLabel("Name").fill("Bad Name")
      await expect(form.getByText("Use lowercase letters, digits, - and _, starting with a letter or _")).toBeVisible()
      await form.getByLabel("Password", { exact: true }).fill("longenough1")
      await form.getByLabel("Confirm the password").fill("different")
      await expect(form.getByText("The passwords do not match")).toBeVisible()
      await form.getByLabel("Public key (optional)").fill("not a key")
      await expect(form.getByText("Not a public key line")).toBeVisible()
      await snap(page, here, "users", "add-user-invalid")
      await form.getByLabel("Public key (optional)").fill(rsaLine(2048))
      await expect(form.getByText("RSA keys need at least 3072 bits")).toBeVisible()
      await snap(page, here, "users", "add-user-short-rsa")
      await form.getByLabel("Name").fill("carol")
      await form.getByLabel("Confirm the password").fill("longenough1")
      await form.getByLabel("Public key (optional)").fill(ed25519Line("carol@laptop"))
      await expect(form.getByText("Not a public key line")).toHaveCount(0)
      await snap(page, here, "users", "add-user")
      await page.keyboard.press("Escape")
      await expect(add).toBeFocused()

      await page.getByRole("button", { name: "deploy", exact: true }).click()
      const sheet = dialogOf(page, "deploy")
      const keys = sheet.getByRole("table", { name: "Authorized keys" })
      await expect(keys.getByText("deploy@laptop")).toBeVisible()
      await sheet.getByLabel("Add a public key").fill("garbage")
      await expect(sheet.getByText("Not a public key line")).toBeVisible()
      await snap(page, here, "users", "user-sheet")
      await page.keyboard.press("Escape")
      await expect(sheet).toBeHidden()

      await page.getByRole("button", { name: "Actions for alice", exact: true }).click()
      await page.getByRole("menuitem", { name: "Delete", exact: true }).click()
      const del = dialogOf(page, "Delete alice")
      await expect(del.getByLabel(/to confirm$/)).toBeVisible()
      await snap(page, here, "users", "delete-dialog")
      await page.keyboard.press("Escape")

      await page.route(planRoute("users.sshd.apply"), (route) => {
        const overridden = (route.request().postData() ?? "").includes('"overrides":["users.no_way_in"]')
        return route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(
            plan({
              action: "users.sshd.apply",
              diff: [{ op: "~", text: "PasswordAuthentication yes -> no" }],
              guards: [
                {
                  id: "users.no_way_in",
                  ok: overridden,
                  overridden,
                  overridable: true,
                  message: "No account could sign in over SSH after this change. Considered: root (no key), deploy (no key), alice (locked), backup (no shell).",
                },
              ],
            }),
          ),
        })
      })
      await page.route(applyRoute("users.sshd.apply"), (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(pendingChange("users.sshd.apply", 120)) }))
      await page.getByRole("button", { name: "Edit hardening", exact: true }).click()
      const hardening = dialogOf(page, "Edit SSH hardening")
      await expect(hardening.getByLabel("Login attempts")).toHaveValue("6")
      await expect(hardening.getByRole("switch", { name: "Password login" })).toBeChecked()
      await snap(page, here, "users", "hardening-form")
      await hardening.getByRole("switch", { name: "Password login" }).click()
      await hardening.getByRole("button", { name: "Review change" }).click()
      const review = dialogOf(page, "Apply SSH hardening")
      await expect(review.getByText("Open a new SSH session before you confirm")).toBeVisible()
      await expect(review.getByText("Considered: root (no key), deploy (no key), alice (locked), backup (no shell).", { exact: false })).toBeVisible()
      await expect(review.getByLabel("Override users.no_way_in")).toBeVisible()
      await snap(page, here, "users", "hardening-guard")
      await review.getByLabel("Override users.no_way_in").click()
      const apply = review.getByRole("button", { name: "Apply", exact: true })
      await expect(apply).toBeEnabled()
      await apply.click()
      await expect(review.getByRole("timer")).toContainText("Reverts automatically in")
      await expect(review.getByRole("button", { name: "Confirm", exact: true })).toBeVisible()
      await expect(review.getByText("Open a new SSH session before you confirm")).toBeVisible()
      await snap(page, here, "users", "hardening-countdown")
    })

    // UI-4, UX-6: fail2ban worst case (500 bans, 12 long jails), not installed, CrowdSec, states, the ignore list.
    test(`fail2ban at ${label}`, async ({ page }) => {
      await boot(page, { ...here, worst: true })
      const jails = Array.from({ length: 12 }, (_, i) => ({ name: `${`jail-${i}-`.padEnd(24, "x")}`, banned: 4000 + i, failed: 99_999, failedTotal: 123_456_789 }))
      await page.route(readRoute("fail2ban.status.get"), (route) => route.fulfill(ok({ ...m11.fail2banInstalled, jails })))
      const bans = m11.bansWorst.map((b, i) => ({ ...b, address: i % 5 === 0 ? b.address : `203.0.${113 + Math.floor(i / 250)}.${i % 250}` }))
      await page.route(readRoute("fail2ban.ban.list"), (route) => route.fulfill(ok({ bans })))
      await page.goto(tab("fail2ban"))
      await expect(page.getByRole("heading", { name: "Banned addresses", exact: true })).toBeVisible()
      await expect(page.getByRole("table", { name: "Banned addresses" }).locator("tbody tr")).toHaveCount(500)
      await expect(page.getByText("2001:0db8:85a3:0000:0000:8a2e:0370:7000").first()).toBeVisible()
      await snap(page, here, "fail2ban", "", false)
      await page.unroute(readRoute("fail2ban.status.get"))
      await page.unroute(readRoute("fail2ban.ban.list"))
      await page.route(readRoute("fail2ban.ban.list"), (route) => route.fulfill(ok({ bans: m11.bansDefault })))

      await forced(page, "fail2ban.status.get", "loading", () => page.reload(), async () => {
        await expect(page.getByRole("status").first()).toBeVisible()
        await snap(page, here, "fail2ban", "loading")
      })
      await forced(page, "fail2ban.status.get", "error", () => page.reload(), async () => {
        await expect(page.getByText("Couldn't load intrusion prevention", { exact: true })).toBeVisible()
        await snap(page, here, "fail2ban", "error")
      })

      await page.route(readRoute("fail2ban.status.get"), (route) => route.fulfill(ok(m11.fail2banNotInstalled)))
      await page.reload()
      await expect(page.getByRole("heading", { name: "fail2ban is not installed", exact: true })).toBeVisible()
      await snap(page, here, "fail2ban", "not-installed")
      await page.getByRole("button", { name: "Install fail2ban", exact: true }).click()
      await expect(dialogOf(page, "Install fail2ban")).toBeVisible()
      await snap(page, here, "fail2ban", "install-dialog")
      await page.keyboard.press("Escape")
      await page.unroute(readRoute("fail2ban.status.get"))

      await page.route(readRoute("fail2ban.status.get"), (route) => route.fulfill(ok({ ...m11.fail2banNotInstalled, crowdsec: true })))
      await page.reload()
      await expect(page.getByText("CrowdSec runs on this host", { exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Install fail2ban" })).toHaveCount(0)
      await snap(page, here, "fail2ban", "crowdsec-not-installed")
      await page.unroute(readRoute("fail2ban.status.get"))

      await page.route(readRoute("fail2ban.status.get"), (route) => route.fulfill(ok(m11.fail2banCrowdsec)))
      await page.reload()
      await expect(page.getByText("CrowdSec runs on this host", { exact: true })).toBeVisible()
      await expect(page.getByRole("heading", { name: "Jails", exact: true })).toBeVisible()
      await snap(page, here, "fail2ban", "crowdsec")
      await page.unroute(readRoute("fail2ban.status.get"))

      const noBans = (route: Route) => route.fulfill(ok({ bans: [] }))
      await page.route(readRoute("fail2ban.ban.list"), noBans)
      await page.reload()
      await expect(page.getByText("No address is banned", { exact: true })).toBeVisible()
      await page.unroute(readRoute("fail2ban.ban.list"), noBans)

      await page.reload()
      const settings = page.getByRole("heading", { name: "Settings", exact: true })
      await expect(settings).toBeVisible()
      for (const fixed of m11.fail2banInstalled.ignoreFixed) {
        await expect(page.getByText(fixed, { exact: true }).first()).toBeVisible()
        await expect(page.getByRole("button", { name: `Remove ${fixed}`, exact: true }), `${fixed} is a read-only chip`).toHaveCount(0)
      }
      await expect(page.getByRole("button", { name: "Remove 198.51.100.0/24", exact: true })).toBeVisible()
      const entry = page.getByLabel("Add an address or network")
      for (const bad of ["not-an-ip", "999.1.1.1", "10.0.0.0/33"]) {
        await entry.fill(bad)
        await page.keyboard.press("Enter")
        await expect(page.getByText("Enter an IPv4 or IPv6 address or network", { exact: true }), `${bad} is refused`).toBeVisible()
      }
      await snap(page, here, "fail2ban", "ignore-invalid")
      for (const good of ["10.0.0.0/8", "2001:db8::/32", "192.0.2.77"]) {
        await entry.fill(good)
        await page.keyboard.press("Enter")
        await expect(page.getByRole("button", { name: `Remove ${good}`, exact: true }), `${good} is accepted`).toBeVisible()
      }
      await snap(page, here, "fail2ban", "settings")
    })

    // UX-1, UX-6: an operator unbans without a typed phrase; the viewer's pages have no actions.
    test(`roles at ${label}`, async ({ page }) => {
      await boot(page, { ...here, m11: "operate", jobs: true })
      await page.goto(tab("services"))
      await expect(page.getByRole("button", { name: "Actions for nginx.service", exact: true })).toBeVisible()
      await page.getByRole("button", { name: "Actions for nginx.service", exact: true }).click()
      for (const item of ["Start", "Stop", "Restart", "Reload", "View logs"]) await expect(page.getByRole("menuitem", { name: item, exact: true })).toBeVisible()
      for (const gone of ["Enable", "Disable"]) await expect(page.getByRole("menuitem", { name: gone, exact: true }), `an operator has no ${gone}`).toHaveCount(0)
      await snap(page, here, "services", "operator-menu")
      await page.keyboard.press("Escape")
      await page.goto(tab("users"))
      await expect(page.getByRole("table", { name: "Login accounts" })).toBeVisible()
      await expect(page.getByRole("button", { name: "Add user" })).toHaveCount(0)
      await expect(page.getByRole("button", { name: /^Actions for / })).toHaveCount(0)
      await expect(page.getByRole("button", { name: "Edit hardening" })).toHaveCount(0)
      await snap(page, here, "users", "operator")
      await page.goto(tab("fail2ban"))
      const unban = page.getByRole("button", { name: "Unban 198.51.100.201 from sshd", exact: true })
      await expect(unban).toBeVisible()
      await expect(page.getByRole("heading", { name: "Settings", exact: true })).toHaveCount(0)
      await snap(page, here, "fail2ban", "operator")
      await unban.click()
      const dialog = dialogOf(page, "Unban 198.51.100.201")
      await expect(dialog.getByRole("button", { name: "Apply", exact: true })).toBeEnabled()
      await expect(dialog.getByLabel(/to confirm$/)).toHaveCount(0)
      await snap(page, here, "fail2ban", "unban-dialog")
      await page.keyboard.press("Escape")
      await page.goto(tab("jobs"))
      await expect(page.getByRole("table", { name: "Darkpanel jobs" })).toBeVisible()
      await expect(page.getByRole("button", { name: "Add job" })).toHaveCount(0)
      await expect(page.getByRole("button", { name: /^Actions for / })).toHaveCount(0)
      await snap(page, here, "jobs", "operator")
      await page.goto(tab("tuning"))
      await expect(page.getByRole("table", { name: "Kernel parameters" })).toBeVisible()
      await expect(page.getByRole("button", { name: "Review changes" })).toHaveCount(0)
      await expect(page.getByLabel("vm.swappiness at boot")).toHaveCount(0)
      await snap(page, here, "tuning", "operator")
      await page.goto(tab("services"))
      await expect(page.getByRole("link", { name: "Console", exact: true }), "members never see the Console tab").toHaveCount(0)
    })

    test(`viewer at ${label}`, async ({ page }) => {
      await boot(page, { ...here, m11: "read", jobs: true })
      // The agent leaves the command text out of a viewer's job list.
      await page.route(readRoute("jobs.job.list"), (route) =>
        route.fulfill(ok({ ...m11.jobsDefault, jobs: m11.jobsDefault.jobs.map((j) => withoutCommand(j)), cron: m11.jobsDefault.cron.map((c) => withoutCommand(c)) })),
      )
      await page.goto(tab("services"))
      await expect(page.getByRole("heading", { name: "Journal", exact: true })).toBeVisible()
      await expect(page.locator("main table").first().locator("tbody tr").first()).toBeVisible()
      await expect(page.getByRole("button", { name: /^Actions for / })).toHaveCount(0)
      await expect(page.getByRole("columnheader", { name: "Actions" })).toHaveCount(0)
      await snap(page, here, "services", "viewer")
      await page.goto(tab("users"))
      await expect(page.getByRole("table", { name: "Login accounts" })).toBeVisible()
      await expect(page.getByRole("button", { name: /^Actions for |^Add user$|^Edit hardening$/ })).toHaveCount(0)
      await snap(page, here, "users", "viewer")
      await page.goto(tab("fail2ban"))
      await expect(page.getByRole("table", { name: "Banned addresses" })).toBeVisible()
      await expect(page.getByRole("button", { name: /^Unban/ })).toHaveCount(0)
      await snap(page, here, "fail2ban", "viewer")
      await page.goto(tab("jobs"))
      await expect(page.getByRole("table", { name: "Darkpanel jobs" })).toBeVisible()
      await expect(page.getByRole("button", { name: /^Actions for |^Add job$/ })).toHaveCount(0)
      await expect(page.getByText("pg-backup")).toHaveCount(0)
      await expect(page.getByText("certbot -q renew")).toHaveCount(0)
      await snap(page, here, "jobs", "viewer")
      await page.goto(tab("tuning"))
      await expect(page.getByRole("table", { name: "Kernel parameters" })).toBeVisible()
      await expect(page.getByRole("button", { name: "Review changes" })).toHaveCount(0)
      await expect(page.getByLabel(/ at boot$/)).toHaveCount(0)
      await expect(page.getByRole("button", { name: /^Create swap file$|^Resize$|^Remove$/ })).toHaveCount(0)
      await snap(page, here, "tuning", "viewer")
    })

    // UI-7, UX-10: 100 jobs with 40-character names and long commands, the form, states, Run now, View output.
    test(`jobs at ${label}`, async ({ page }) => {
      await boot(page, { ...here, worst: true, jobs: true })
      await page.goto(tab("jobs"))
      const table = page.getByRole("table", { name: "Darkpanel jobs" })
      await expect(table.locator("tbody tr")).toHaveCount(100)
      await expect(page.getByRole("table", { name: "Other scheduled tasks" })).toBeVisible()
      await expect(page.getByText("/etc/cron.d/certbot").first()).toBeVisible()
      await expect(table.getByText("Failed", { exact: false }).first()).toBeVisible()
      await snap(page, here, "jobs", "", false)

      await forced(page, "jobs.job.list", "loading", () => page.reload(), async () => {
        await expect(page.getByRole("status", { name: "Loading the jobs" })).toBeVisible()
        await snap(page, here, "jobs", "loading")
      })
      await forced(page, "jobs.job.list", "error", () => page.reload(), async () => {
        await expect(page.getByText("Couldn't load the jobs", { exact: true })).toBeVisible()
        await snap(page, here, "jobs", "error")
      })
      await page.route(readRoute("jobs.job.list"), (route) => route.fulfill(ok({ ...m11.jobsEmpty, cron: [] })))
      await page.reload()
      await expect(page.getByText("No Darkpanel jobs yet", { exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Add job", exact: true })).toBeVisible()
      await snap(page, here, "jobs", "empty")
      await page.unroute(readRoute("jobs.job.list"))
    })

    test(`jobs form and flows at ${label}`, async ({ page }) => {
      await boot(page, { ...here, jobs: true })
      await page.route(readRoute("jobs.schedule.check"), (route) => {
        const schedule = String(((route.request().postDataJSON() as { input?: { schedule?: string } }).input ?? {}).schedule ?? "")
        return /bogus/.test(schedule) ? route.fulfill(problem(422, "request.invalid_input", "Not a schedule systemd understands")) : route.fulfill(ok(m11.scheduleNext))
      })
      await page.goto(tab("jobs"))
      const table = page.getByRole("table", { name: "Darkpanel jobs" })
      await expect(table.getByText("db-backup", { exact: true })).toBeVisible()
      await expect(page.getByText("Every day at 03:00", { exact: false }).or(page.getByText("*-*-* 03:00:00").first()).first()).toBeVisible()
      await snap(page, here, "jobs", "default")

      const add = page.getByRole("button", { name: "Add job", exact: true })
      await add.click()
      const form = dialogOf(page, /^Add a job on /)
      await expect(form.getByLabel("Name")).toBeFocused()
      await form.getByLabel("Name").fill("nightly-backup")
      await form.getByLabel("Schedule").fill("bogus schedule")
      await expect(form.getByText("Not a schedule systemd understands")).toBeVisible()
      await form.getByLabel("Command").fill("/usr/local/bin/backup --all")
      await snap(page, here, "jobs", "form-invalid-schedule")
      await form.getByLabel("Schedule").fill("daily")
      await expect(form.getByRole("list", { name: "Next runs" }).getByRole("listitem")).toHaveCount(3)
      await expect(form.getByText("Catch up a run missed while the host was off")).toBeVisible()
      await snap(page, here, "jobs", "form")
      await page.keyboard.press("Escape")
      await expect(add).toBeFocused()

      await page.getByRole("button", { name: "Actions for db-backup", exact: true }).click()
      for (const item of ["Run now", "Edit", "View output", "Disable", "Delete"]) await expect(page.getByRole("menuitem", { name: item, exact: true })).toBeVisible()
      await page.getByRole("menuitem", { name: "Run now", exact: true }).click()
      const run = dialogOf(page, "Run db-backup now")
      await run.getByRole("button", { name: "Apply", exact: true }).click()
      await expect(page.locator(".dr-toast-title", { hasText: "Started" })).toBeVisible()
      await page.keyboard.press("Escape")

      await page.getByRole("button", { name: "Actions for db-backup", exact: true }).click()
      await page.getByRole("menuitem", { name: "View output", exact: true }).click()
      const output = page.getByRole("region", { name: "Output of darkpanel-job-db-backup.service" })
      await expect(output).toBeFocused()
      await expect(output.getByLabel("Unit", { exact: true })).toHaveValue("darkpanel-job-db-backup.service")
      await snap(page, here, "jobs", "view-output")
    })

    // UI-8, UX-11: swap and every kernel key, locked, overridden and unavailable ones.
    test(`tuning at ${label}`, async ({ page }) => {
      await boot(page, here)
      const keys = Array.from({ length: 60 }, (_, i) => ({
        key: i === 0 ? `net.ipv4.conf.${"very-long-interface-name-".repeat(3)}rp_filter` : `net.ipv4.neigh.default.gc_thresh${i}`,
        help: i % 3 === 0 ? "How readily the kernel moves memory to swap and how long the cache lives, in pages." : "",
        kind: "integer",
        min: "0",
        max: "65535",
        choices: [],
        live: String(1000 + i),
        set: i % 4 === 1 ? String(2000 + i) : "",
        laterFiles: i % 7 === 3 ? ["/etc/sysctl.d/99-zzz-a-late-override-with-a-long-name.conf"] : [],
        owner: i % 5 === 2 ? "docker-compose-networking" : "",
        unavailable: i % 11 === 4 ? "Not in this kernel" : "",
      }))
      const swaps = Array.from({ length: 5 }, (_, i) => ({ path: `/mnt/volumes/${"deep/".repeat(12)}swapfile-${i}`, type: i % 2 === 0 ? "file" : "partition", size: 4096 * 1024 * 1024, used: i * 700 * 1024 * 1024, darkpanel: i === 4 }))
      await page.route(readRoute("tuning.sysctl.get"), (route) => route.fulfill(ok({ ...m11.sysctlDefault, keys })))
      await page.route(readRoute("tuning.swap.get"), (route) => route.fulfill(ok({ ...m11.swapDefault, swaps })))
      await page.goto(tab("tuning"))
      await expect(page.getByRole("table", { name: "Kernel parameters" }).locator("tbody tr")).toHaveCount(60)
      await expect(page.getByText("Managed by docker-compose-networking").first()).toBeVisible()
      await expect(page.getByText(/sets it later at boot and wins/).first()).toBeVisible()
      await snap(page, here, "tuning", "", false)
      await page.unroute(readRoute("tuning.sysctl.get"))
      await page.unroute(readRoute("tuning.swap.get"))

      await forced(page, "tuning.sysctl.get", "loading", () => page.reload(), async () => {
        await expect(page.getByRole("status", { name: "Loading kernel parameters" })).toBeVisible()
        await snap(page, here, "tuning", "loading")
      })
      await forced(page, "tuning.sysctl.get", "error", () => page.reload(), async () => {
        await expect(page.getByText(/Couldn't load kernel parameters/)).toBeVisible()
        await snap(page, here, "tuning", "error")
      })
      await page.reload()
      await expect(page.getByLabel("vm.swappiness at boot")).toHaveValue("10")
      await expect(page.getByText("Managed by docker", { exact: true })).toBeVisible()
      await expect(page.getByText("/etc/sysctl.d/99-zzz-local.conf sets it later at boot and wins")).toBeVisible()
      await snap(page, here, "tuning", "default")

      const somaxconn = page.getByLabel("net.core.somaxconn at boot")
      await somaxconn.fill("99999999")
      await expect(page.getByText("Use 128 to 65535", { exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Review changes" })).toBeDisabled()
      await snap(page, here, "tuning", "out-of-range")
      await somaxconn.fill("8192")
      await page.getByLabel("vm.swappiness at boot").fill("20")
      await expect(page.getByText("2 changed", { exact: true })).toBeVisible()
      const planned: string[] = []
      page.on("request", (r) => {
        if (r.method() === "POST" && r.url().includes("/tuning.sysctl.apply/v1/plan")) planned.push(r.url())
      })
      await page.getByRole("button", { name: "Review changes" }).click()
      const review = dialogOf(page, "Set 2 kernel parameters")
      await expect(review.getByRole("button", { name: "Apply", exact: true })).toBeEnabled()
      expect(planned, "one plan for every changed key").toHaveLength(1)
      await snap(page, here, "tuning", "review-dialog")
      await review.getByRole("button", { name: "Apply", exact: true }).click()
      await expect(review.getByRole("timer")).toContainText("Reverts automatically")
      await snap(page, here, "tuning", "revertible-countdown")
      await page.keyboard.press("Escape")

      await page.route(readRoute("tuning.swap.get"), (route) => route.fulfill(ok(m11.swapUnsupported)))
      await page.reload()
      await expect(page.getByText("The root filesystem is btrfs, which cannot hold a swap file here.")).toBeVisible()
      await snap(page, here, "tuning", "unsupported-filesystem")
      await page.unroute(readRoute("tuning.swap.get"))

      await page.route(planRoute("tuning.swap.remove"), (route) => route.fulfill(problem(422, "tuning.swap_in_use", "The swap file holds 1.2 GiB in use and memory has only 0.4 GiB free; stop a service first")))
      await page.reload()
      await page.getByRole("button", { name: "Remove", exact: true }).click()
      await expect(page.getByText("memory has only 0.4 GiB free", { exact: false })).toBeVisible()
      await snap(page, here, "tuning", "swap-guard")
      await page.keyboard.press("Escape")
      const size = page.getByLabel("/darkpanel.swap size (MiB)")
      await size.fill("100")
      await page.getByRole("button", { name: "Resize", exact: true }).click()
      await expect(page.getByText(/^Use 256 to \d+ MiB$/)).toBeVisible()
      await snap(page, here, "tuning", "swap-resize-refused")
    })

    // UX-12: Console and Jobs off on the host; admins can enable, members read the text.
    test(`module off at ${label}`, async ({ page }) => {
      await boot(page, here)
      await page.route(planRoute("core.module.enable"), (route) =>
        route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(plan({ hostId: null, action: "core.module.enable", diff: [{ op: "~", text: "console on web-1: off -> on" }] })) }),
      )
      await page.goto(tab("console"))
      await expect(page.getByText("Off on this host", { exact: true })).toBeVisible()
      expect(await page.getByRole("link", { name: "Console", exact: true }).count(), "an admin sees the Console tab").toBeGreaterThan(0)
      await expect(page.getByText("Turning it on lets admins open a root terminal and manage files on this host.")).toBeVisible()
      await snap(page, here, "console", "off")
      await page.getByRole("button", { name: "Enable", exact: true }).click()
      const enable = dialogOf(page, "Enable console on web-1")
      await expect(enable.getByRole("button", { name: "Apply", exact: true })).toBeEnabled()
      await snap(page, here, "console", "enable-dialog")
      await page.keyboard.press("Escape")
      await page.goto(tab("jobs"))
      await expect(page.getByText("Off on this host", { exact: true })).toBeVisible()
      await expect(page.getByText("Turning it on lets admins schedule commands to run on this host.")).toBeVisible()
      await expect(page.getByRole("button", { name: "Enable", exact: true })).toBeVisible()
      await snap(page, here, "jobs", "off")
    })

    test(`module off for a member at ${label}`, async ({ page }) => {
      await boot(page, { ...here, m11: "operate" })
      await page.goto(tab("jobs"))
      await expect(page.getByText("Off on this host", { exact: true })).toBeVisible()
      await expect(page.getByText("Turning it on lets admins schedule commands to run on this host.")).toBeVisible()
      await expect(page.getByRole("button", { name: "Enable", exact: true })).toHaveCount(0)
      await snap(page, here, "jobs", "off-member")
    })

    // UI-6, UX-7: the step-up dialog and each answer to a code.
    test(`step-up dialog at ${label}`, async ({ page }) => {
      await boot(page, { ...here, console: true })
      const ticket = /\/channels\/console\/terminal\/ticket$/
      await page.route(ticket, (route) => route.fulfill(problem(401, "auth.invalid_credentials", "wrong authenticator code")))
      await page.goto(tab("console"))
      const dialog = dialogOf(page, "Enter your authenticator code")
      const field = dialog.getByLabel("Authenticator code")
      await expect(field).toBeFocused()
      await expect(dialog.getByText("Opening a terminal needs your authenticator code")).toBeVisible()
      await expect(dialog.getByRole("button", { name: "Verify", exact: true })).toBeDisabled()
      await snap(page, here, "stepup", "asking")
      await page.keyboard.type("000000")
      await expect(dialog.getByRole("button", { name: "Verify", exact: true })).toBeEnabled()
      await page.keyboard.press("Enter")
      await expect(dialog.getByText("That code didn't work", { exact: true })).toBeVisible()
      await expect(field).toBeFocused()
      await expect(field).toHaveValue("")
      await snap(page, here, "stepup", "wrong-code")
      await page.unroute(ticket)
      await page.route(ticket, (route) => route.fulfill(problem(401, "auth.step_up_wait", "that code was already used")))
      await page.keyboard.type("111111")
      await page.keyboard.press("Enter")
      await expect(dialog.getByText("Wait for the next code", { exact: true })).toBeVisible()
      await snap(page, here, "stepup", "used")
      await page.unroute(ticket)
      await page.route(ticket, (route) => route.fulfill(problem(429, "auth.rate_limited", "too many attempts", { "Retry-After": "30" })))
      await page.keyboard.type("222222")
      await page.keyboard.press("Enter")
      await expect(dialog.getByText("Too many attempts; try again in 30 s", { exact: true })).toBeVisible()
      await snap(page, here, "stepup", "rate-limited")
      await page.unroute(ticket)
      await page.route(ticket, (route) => route.fulfill(problem(403, "auth.step_up_unavailable", "no authenticator")))
      await page.keyboard.type("333333")
      await page.keyboard.press("Enter")
      await expect(dialog.getByText("Set up an authenticator to use the console and jobs", { exact: true })).toBeVisible()
      await expect(dialog.getByLabel("Authenticator code")).toHaveCount(0)
      await snap(page, here, "stepup", "no-authenticator")
      await page.keyboard.press("Escape")
      await expect(dialog).toBeHidden()
      await expect(page.getByText("The terminal was not opened", { exact: true })).toBeVisible()
    })

    // UX-7: Escape cancels a step-up asked while a form is open and focus goes back to the button that opened the form.
    test(`step-up escape returns focus at ${label}`, async ({ page }) => {
      await boot(page, here)
      await page.goto(tab("users"))
      const add = page.getByRole("button", { name: "Add user", exact: true })
      await expect(add).toBeVisible()
      await add.focus()
      await page.keyboard.press("Enter")
      const form = dialogOf(page, "Add a user")
      await form.getByLabel("Name").fill("dave")
      await form.getByRole("button", { name: "Review change" }).click()
      const dialog = dialogOf(page, "Enter your authenticator code")
      await expect(dialog.getByLabel("Authenticator code")).toBeFocused()
      await page.keyboard.press("Escape")
      await expect(dialog).toBeHidden()
      await expect(add, "focus returns to the button that opened the form").toBeFocused()
    })

    // UI-5, UX-8: the terminal connected, its toolbar, key bar, resize and focus shortcut.
    test(`terminal connected at ${label}`, async ({ page }) => {
      await boot(page, { ...here, console: true })
      const resizes: { cols: number; rows: number }[] = []
      const urls: string[] = []
      const xterm: string[] = []
      page.on("request", (r) => {
        if (/xterm/i.test(r.url())) xterm.push(r.url())
      })
      await page.routeWebSocket(/\/api\/v1\/hosts\/[^/]+\/channels\/console\/terminal\?/, (ws) => {
        urls.push(ws.url())
        let line = ""
        ws.send(Buffer.from("$ "))
        ws.onMessage((message) => {
          if (typeof message === "string") {
            const m = JSON.parse(message) as { type?: string; cols?: number; rows?: number }
            if (m.type === "resize") resizes.push({ cols: m.cols ?? 0, rows: m.rows ?? 0 })
            return
          }
          for (const ch of message.toString("utf8")) {
            if (ch === "\r") {
              ws.send(Buffer.from(`\r\n${line.startsWith("echo ") ? `${line.slice(5)}\r\n` : ""}$ `))
              line = ""
            } else {
              line += ch
              ws.send(Buffer.from(ch))
            }
          }
        })
      })
      await page.goto(tab("services"))
      await expect(page.getByRole("heading", { name: "Journal", exact: true })).toBeVisible()
      expect(xterm, "xterm is not fetched outside the console route").toEqual([])
      await page.goto(tab("console"))
      await expect(dialogOf(page, "Enter your authenticator code")).toBeVisible()
      await settle(page)
      await snap(page, here, "console", "step-up")
      await enterCode(page)
      const toolbar = page.getByRole("toolbar", { name: "Terminal" })
      await expect(toolbar.getByText("Connected", { exact: true })).toBeVisible()
      expect(xterm.length, "xterm loads with the console route").toBeGreaterThan(0)
      await expect(toolbar.getByText("web-1", { exact: true })).toBeVisible()
      await expect(toolbar.getByRole("button", { name: "Close" })).toBeVisible()
      await expect(toolbar.getByText("Ctrl+Shift+L leaves the terminal")).toBeVisible()
      const keyBar = page.getByRole("group", { name: "Terminal keys" })
      if (vp.width < 768) {
        for (const key of ["Esc", "Tab", "Ctrl", "Up arrow", "Down arrow", "Left arrow", "Right arrow"]) await expect(keyBar.getByRole("button", { name: key, exact: true })).toBeVisible()
      } else {
        await expect(keyBar).toHaveCount(0)
      }
      const area = page.getByTestId("terminal-area")
      const box = await area.boundingBox()
      expect(box?.height ?? 0, "the xterm area fills the remaining height").toBeGreaterThan(200)
      await page.keyboard.type("echo hi")
      await page.keyboard.press("Enter")
      await expect(page.locator(".xterm-rows > div").filter({ hasText: /^hi\s*$/ })).toHaveCount(1)
      await snap(page, here, "console", "terminal-connected")
      const before = resizes.length
      await page.setViewportSize({ width: vp.width - 40, height: vp.height - 100 })
      await expect.poll(() => resizes.length, "the xterm resizes with the panel").toBeGreaterThan(before)
      await page.setViewportSize(vp)
      expect(urls[0]).not.toContain(CODE)
      await page.keyboard.press("Control+Shift+L")
      await expect(toolbar).toBeFocused()
      await page.getByRole("button", { name: "Close" }).click()
      await expect(page.getByText("You closed the terminal", { exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Reconnect" })).toBeVisible()
      await snap(page, here, "console", "terminal-closed")
    })

    // UX-8: the connecting state, while the socket's open is held back.
    test(`terminal connecting at ${label}`, async ({ page }) => {
      // Playwright installs its own WebSocket wrapper for the route, which the held one must extend.
      await page.routeWebSocket(/\/api\/v1\/hosts\/[^/]+\/channels\/console\/terminal\?/, (ws) => ws.send(Buffer.from("$ ")))
      await boot(page, { ...here, console: true })
      await page.addInitScript(() => ((window as unknown as { __wsHold: boolean }).__wsHold = true))
      await page.goto(tab("console"))
      await enterCode(page)
      await expect(page.getByRole("toolbar", { name: "Terminal" }).getByText("Connecting…", { exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Close" })).toBeVisible()
      await snap(page, here, "console", "terminal-connecting")
    })

    // UX-8: each close reason and whether Reconnect is offered.
    test(`terminal closed states at ${label}`, async ({ page }) => {
      await boot(page, { ...here, console: true })
      let reason = "idle"
      let exit: number | null = null
      await page.routeWebSocket(/\/api\/v1\/hosts\/[^/]+\/channels\/console\/terminal\?/, async (ws) => {
        ws.send(Buffer.from("$ "))
        if (exit !== null) ws.send(JSON.stringify({ type: "exit", code: exit }))
        await new Promise((r) => setTimeout(r, 400))
        ws.close({ code: 1000, reason })
      })
      const cases: [string, number | null, string, boolean][] = [
        ["idle", null, "Closed after 30 minutes without input", true],
        ["expired", null, "Your sign-in session ended; sign in again to open a terminal", false],
        ["console.limit", null, "Too many open terminals; close one, then reconnect", true],
        ["agent restarted", null, "The agent restarted; open a new terminal", true],
        ["revoked", null, "Your access to this host was revoked", false],
        ["ended", 130, "The shell exited with code 130", true],
      ]
      for (const [why, code, text, reconnect] of cases) {
        reason = why
        exit = code
        await page.goto(tab("console"))
        await enterCode(page)
        await expect(page.getByText(text, { exact: true })).toBeVisible()
        await expect(page.getByRole("toolbar", { name: "Terminal" }).getByText("Closed", { exact: true })).toBeVisible()
        await expect(page.getByRole("button", { name: "Reconnect" }), `${why}: Reconnect ${reconnect ? "is" : "is not"} offered`).toHaveCount(reconnect ? 1 : 0)
        await snap(page, here, "console", `terminal-closed-${why.replace(/[ .]/g, "-")}`)
      }
      reason = "idle"
      exit = null
      await page.goto(tab("console"))
      await enterCode(page)
      await page.getByRole("button", { name: "Reconnect" }).click({ timeout: 10_000 })
      await expect(dialogOf(page, "Enter your authenticator code"), "Reconnect asks a new code").toBeVisible()
    })

    test(`terminal host offline at ${label}`, async ({ page }) => {
      await boot(page, { ...here, console: true, editHost: (h) => (h.id === HOST_ID ? { ...h, online: false } : h) })
      await page.goto(tab("console"))
      await expect(page.getByText("The host is offline; reconnect once it is back", { exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Reconnect" })).toBeVisible()
      await snap(page, here, "console", "terminal-host-offline")
    })

    // UI-5, UX-9: the file manager on a 2000-entry folder with 255-byte and non-UTF-8 names.
    test(`files worst case at ${label}`, async ({ page }) => {
      await boot(page, { ...here, console: true })
      await page.route(/\/files\/list/, (route) => {
        const q = new URL(route.request().url()).searchParams
        if (q.get("path") !== "/srv/uploads") return route.fallback()
        if (q.get("cursor") === "cursor-2000") return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ path: "/srv/uploads", entries: m11.filesWorst.entries.slice(0, 3).map((e) => ({ ...e, name: `more-${e.name}` })) }) })
        return route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ ...m11.filesWorst, entries: m11.filesWorst.entries.map((e, i) => (e.raw === undefined ? e : { ...e, raw: `${e.raw}${i}` })) }) })
      })
      await page.goto(tab("console", "?path=/srv/uploads"))
      const table = page.getByRole("table", { name: "Files in /srv/uploads" })
      await expect(table).toBeVisible()
      await expect(table.locator("tbody tr")).toHaveCount(2000 + 1)
      await expect(page.getByRole("button", { name: "Show more", exact: true })).toBeVisible()
      const long = table.getByText(/^upload-101-segment/).first()
      await expect(long).toBeVisible()
      expect(((await long.textContent()) ?? "").length, "a 255-byte name").toBe(255)
      await expect(table.getByText("report-\\xff\\xfe-7.csv", { exact: true })).toBeVisible()
      await snap(page, here, "files", "", false)
      await page.getByRole("button", { name: "Actions for report-\\xff\\xfe-7.csv", exact: true }).click()
      await expect(page.getByRole("menuitem", { name: /^Rename/ })).toHaveAttribute("aria-disabled", "true")
      await expect(page.getByText("Its name is not UTF-8; change it in the terminal").first()).toBeVisible()
      await snap(page, here, "files", "escaped-name-menu")
      await page.keyboard.press("Escape")
      await page.getByRole("button", { name: "Show more", exact: true }).click()
      await expect(table.locator("tbody tr")).toHaveCount(2000 + 1 + 3)
      await expect(page.getByRole("button", { name: "Show more", exact: true })).toHaveCount(0)
      await page.getByRole("button", { name: "Actions for upload-0002.bin", exact: true }).click()
      await page.getByRole("menuitem", { name: "Edit", exact: true }).click()
      await expect(page.getByText("This file is not text or is over 1 MiB. Download it instead.")).toBeVisible()
      await snap(page, here, "files", "not-text")
    })

    // UI-5, UX-9: states, the path in the URL, the row menu, protected paths, delete, the editor, a conflict and an upload.
    test(`files states at ${label}`, async ({ page }) => {
      await boot(page, { ...here, console: true })
      const listing = /\/files\/list/
      const never = (route: Route) => (new URL(route.request().url()).searchParams.get("path") === "/etc" ? undefined : route.fallback())
      await page.route(listing, never)
      await page.goto(tab("console", "?path=/etc"))
      await expect(page.getByRole("status", { name: "Loading /etc" })).toBeVisible()
      await snap(page, here, "files", "loading")
      await page.unroute(listing, never)
      const failing = (route: Route) => (new URL(route.request().url()).searchParams.get("path") === "/etc" ? route.fulfill(problem(409, "host.busy", "The host is busy; try again")) : route.fallback())
      await page.route(listing, failing)
      await page.reload()
      await expect(page.getByText("Couldn't list /etc", { exact: true })).toBeVisible()
      await snap(page, here, "files", "error")
      await page.unroute(listing, failing)
      await page.getByRole("button", { name: "Try again" }).click()
      const table = page.getByRole("table", { name: "Files in /etc" })
      await expect(table).toBeVisible()
      await expect(page.getByLabel("Path", { exact: true })).toHaveValue("/etc")
      await expect(page.getByRole("navigation", { name: "Breadcrumb", exact: true })).toBeVisible()
      await expect(page.getByText("Darkpanel's own files are read-only here")).toBeVisible()
      await snap(page, here, "files", "default")

      await page.getByRole("button", { name: "nginx/", exact: true }).click()
      await expect(page).toHaveURL(/[?&]path=%2Fetc%2Fnginx$/)
      await expect(page.getByText("This folder is empty", { exact: true })).toBeVisible()
      await snap(page, here, "files", "empty-folder")
      await page.goBack()
      await expect(page).toHaveURL(/[?&]path=(%2F|\/)etc$/)
      await expect(table).toBeVisible()

      const pathField = page.getByLabel("Path", { exact: true })
      await pathField.focus()
      await page.keyboard.press("ControlOrMeta+A")
      await page.keyboard.type("/etc/ssh")
      await page.keyboard.press("Enter")
      await expect(page).toHaveURL(/[?&]path=%2Fetc%2Fssh$/)
      await page.goBack()

      await page.getByRole("button", { name: "Actions for darkpanel", exact: true }).click()
      await expect(page.getByRole("menuitem", { name: /^Rename/ })).toHaveAttribute("aria-disabled", "true")
      await expect(page.getByRole("menuitem", { name: "Download", exact: true }), "a directory has no download").toHaveCount(0)
      await snap(page, here, "files", "protected-menu")
      await page.keyboard.press("Escape")

      await page.getByRole("button", { name: "Actions for hosts", exact: true }).click()
      for (const item of ["Download", "Edit", "Rename", "Permissions", "Delete"]) await expect(page.getByRole("menuitem", { name: item, exact: true })).toBeVisible()
      await snap(page, here, "files", "menu")
      await page.keyboard.press("Escape")
      await expect(page.getByRole("menu")).toHaveCount(0)

      await page.getByRole("button", { name: "Actions for nginx", exact: true }).click()
      await page.getByRole("menuitem", { name: "Delete", exact: true }).click()
      const del = dialogOf(page, "Delete nginx and everything in it")
      await expect(del.getByText("Type its path, /etc/nginx, to delete it")).toBeVisible()
      await del.getByLabel("Path").fill("/etc/nginx/")
      await expect(del.getByText("Type the path exactly")).toBeVisible()
      await snap(page, here, "files", "delete-directory")
      await page.keyboard.press("Escape")

      await page.getByRole("button", { name: "Actions for hosts", exact: true }).click()
      await page.getByRole("menuitem", { name: "Edit", exact: true }).click()
      await enterCode(page)
      const editor = page.getByRole("textbox", { name: "/etc/hosts", exact: true })
      await expect(editor).toHaveValue("127.0.0.1 localhost\n127.0.1.1 web-1\n")
      await expect(editor, "the editor takes focus once the code is accepted").toBeFocused()
      await page.keyboard.type("10.0.0.12 db-1\n")
      await expect(page.getByText("Unsaved changes")).toBeVisible()
      await snap(page, here, "files", "editor")
      const conflict = (route: Route) => route.fulfill(problem(409, "files.conflict", "Changed on the host"))
      await page.route(/\/files\/write/, conflict)
      await page.getByRole("button", { name: "Save", exact: true }).click()
      await expect(page.getByText("Changed on the host since you opened it", { exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Reload", exact: true })).toBeVisible()
      await expect(page.getByRole("button", { name: "Overwrite", exact: true })).toBeVisible()
      await snap(page, here, "files", "conflict")
      await page.unroute(/\/files\/write/, conflict)
      await page.getByRole("button", { name: "Close", exact: true }).click()

      await page.route(/\/files\/read/, (route) => (new URL(route.request().url()).searchParams.get("path") === "/etc" ? route.fulfill({ status: 200, contentType: "text/plain", body: "" }) : route.fallback()))
      await page.route(/\/files\/upload/, () => {})
      await page.getByTestId("upload-picker").setInputFiles({ name: "backup-2026-11-27.tar", mimeType: "application/x-tar", buffer: Buffer.alloc(4 * 1024 * 1024, 1) })
      await expect(page.getByText("Uploading backup-2026-11-27.tar to /etc", { exact: true })).toBeVisible()
      await expect(page.getByRole("progressbar", { name: "Upload progress" })).toBeVisible()
      await snap(page, here, "files", "upload-progress")
      await page.getByRole("button", { name: "Cancel", exact: true }).click()
      await expect(page.getByText("Uploading backup-2026-11-27.tar to /etc", { exact: true })).toHaveCount(0)

      await page.route(/\/files\/list/, (route) =>
        new URL(route.request().url()).searchParams.get("path") === "/etc/darkpanel"
          ? route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ path: "/etc/darkpanel", entries: [], protected: "Darkpanel's own files are read-only here" }) })
          : route.fallback(),
      )
      await page.goto(tab("console", "?path=/etc/darkpanel"))
      await expect(page.getByRole("button", { name: "Upload", exact: true })).toBeDisabled()
      await expect(page.getByRole("button", { name: "New folder", exact: true })).toBeDisabled()
      await snap(page, here, "files", "protected-folder")
    })
  }
}

// OP-3: copy outside dialogs stays within 90 characters on each new tab.
test("copy on every M11 tab stays within 90 characters", async ({ page }) => {
  await boot(page, { theme: "light", vp: VIEWPORTS[2], console: true, jobs: true })
  const long: string[] = []
  for (const [name, query] of [["services", ""], ["users", ""], ["fail2ban", ""], ["console", ""], ["console", "?path=/etc"], ["jobs", ""], ["tuning", ""]] as const) {
    await page.goto(tab(name, query))
    await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible()
    await settle(page)
    long.push(
      ...(await page.evaluate(() => {
        const out: string[] = []
        for (const el of document.querySelectorAll<HTMLElement>("main *")) {
          if (el.children.length > 0 || el.closest("[role=dialog], [role=log], td, textarea, .font-mono, .xterm, option, select, script, style")) continue
          const text = (el.textContent ?? "").trim()
          if (text.length > 90) out.push(text.slice(0, 60))
        }
        return out
      })),
    )
  }
  expect(long, "no visible copy over 90 characters").toEqual([])
})
