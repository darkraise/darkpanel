import { createReadStream, existsSync, readdirSync, readFileSync, statSync } from "node:fs"
import { createServer, type Server } from "node:http"
import { createRequire } from "node:module"
import { extname, join } from "node:path"
import { expect, test, type Page } from "@playwright/test"
import { HOST_ID, mockApi, type SystemState } from "./fixtures"

test.use({ baseURL: `http://127.0.0.1:${process.env.E2E_PORT ?? "0"}` })

// Acceptance tests of the ui-revamp-server-management spec, browser layer.
// Expectations come from the spec's text; the page is driven through its
// accessible names.

const OFFLINE_HOST_ID = "01JE00000000000000000000H2"
const HOST = "web-1"
const system = `/hosts/${HOST_ID}/system`
const docker = `/hosts/${HOST_ID}/docker`
const VIEWPORTS = [
  { width: 375, height: 812 },
  { width: 768, height: 1024 },
  { width: 1440, height: 900 },
]

const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")

const ROUTES: { name: string; path: string; signedIn?: boolean }[] = [
  { name: "login", path: "/login", signedIn: false },
  { name: "hosts", path: "/" },
  { name: "host overview", path: `/hosts/${HOST_ID}` },
  { name: "host information", path: `/hosts/${HOST_ID}/hostinfo` },
  { name: "system overview", path: system },
  { name: "system updates", path: `${system}/updates` },
  { name: "system packages", path: `${system}/packages` },
  { name: "firewall", path: `/hosts/${HOST_ID}/firewall` },
  { name: "docker", path: docker },
  { name: "tailscale", path: `/hosts/${HOST_ID}/tailscale` },
  { name: "nginx proxy manager", path: `/hosts/${HOST_ID}/npm` },
  { name: "audit", path: "/audit" },
  { name: "rollouts", path: "/rollouts" },
  { name: "tokens", path: "/settings/tokens" },
  { name: "tailnet", path: "/tailnet" },
  { name: "derp", path: "/derp" },
  { name: "cloudflare", path: "/cloudflare" },
]

async function open(page: Page, path: string, opts: { signedIn?: boolean; worstCase?: boolean; systemState?: SystemState } = {}) {
  const fixtures = await mockApi(page, { signedIn: opts.signedIn ?? true, worstCase: opts.worstCase, systemState: opts.systemState })
  await page.goto(path)
  await page.waitForLoadState("networkidle")
  return fixtures
}

async function noHorizontalScroll(page: Page): Promise<boolean> {
  return page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)
}

// ---------------------------------------------------------------------------
// UI-1: theme

test("A2: with empty storage <html> carries data-style glass and data-preset darkpanel before React mounts", async ({ page }) => {
  await mockApi(page, { signedIn: true })
  await page.route(/\/src\/main\.tsx|\/assets\/index-.*\.js/, (r) => r.abort())
  await page.goto("/")
  const attrs = await page.evaluate(() => [localStorage.length, document.documentElement.dataset.style, document.documentElement.dataset.preset, document.getElementById("root")?.childElementCount])
  expect(attrs.slice(1)).toEqual(["glass", "darkpanel", 0])
})

test("A3: the theme switcher shows only mode and accent controls", async ({ page }) => {
  await open(page, "/")
  await page.getByRole("button", { name: "Customize theme" }).click()
  const panel = page.getByRole("dialog").or(page.getByRole("region", { name: /theme/i })).first()
  await expect(page.getByText("Mode", { exact: true })).toBeVisible()
  await expect(page.getByText(/^Accent/)).toBeVisible()
  for (const other of ["Style", "Preset", "Density", "Radius", "Font", "Elevation", "Shell", "Advanced", "Surface"]) {
    await expect(page.getByText(new RegExp(`^${other}`)), `${other} control`).toHaveCount(0)
  }
  void panel
})

// The 7.x key names are taken from the installed package, not assumed.
function installedThemeKeys(): string[] {
  const keys = new Set<string>()
  const walk = (dir: string) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const p = join(dir, e.name)
      if (e.isDirectory()) walk(p)
      else if (/\.(m?js|cjs)$/.test(e.name)) for (const m of readFileSync(p, "utf8").matchAll(/["'`](theme-[a-z][a-z-]*)["'`]/g)) keys.add(m[1]!)
    }
  }
  walk(join("node_modules", "darkraise-ui", "dist"))
  keys.delete("theme-accent")
  return [...keys]
}

test("A4: 6.x and 7.x theme keys are cleared once, mode and theme-accent survive, darkpanel-theme-v2 is set", async ({ page }) => {
  const sixX = ["theme-preset", "theme-radius", "theme-control-depth", "theme-font", "theme-density", "theme-elevation", "theme-button-elevation"]
  const keys = [...new Set([...sixX, ...installedThemeKeys()])]
  expect(keys.length).toBeGreaterThan(10)
  await mockApi(page, { signedIn: true })
  await page.addInitScript(
    ([stale]) => {
      if (sessionStorage.getItem("qa-seeded") !== null) return
      sessionStorage.setItem("qa-seeded", "1")
      for (const k of stale!) localStorage.setItem(k, "garbage")
      localStorage.setItem("mode", "light")
      localStorage.setItem("theme-accent", "rose")
    },
    [keys],
  )
  await page.goto("/")
  await page.waitForLoadState("networkidle")
  const state = () => page.evaluate(() => Object.fromEntries(Object.keys(localStorage).map((k) => [k, localStorage.getItem(k)])))
  const after = await state()
  // The kit may write its own bookkeeping keys back after mounting; a key counts as stale when it still holds the seeded value.
  const left = keys.filter((k) => after[k] === "garbage")
  expect(left, "stale theme keys left after the first load").toEqual([])
  expect(after["mode"]).toBe("light")
  expect(after["theme-accent"]).toBe("rose")
  expect(after["darkpanel-theme-v2"]).not.toBeNull()
  expect(await page.evaluate(() => [document.documentElement.dataset.style, document.documentElement.dataset.preset])).toEqual(["glass", "darkpanel"])
  await page.reload()
  await page.waitForLoadState("networkidle")
  const again = await state()
  expect(again["mode"]).toBe("light")
  expect(again["theme-accent"]).toBe("rose")
})

// A5: the controller's Content-Security-Policy, in force on the built app.
const CSP =
  "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
const MIME: Record<string, string> = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".woff2": "font/woff2", ".svg": "image/svg+xml", ".json": "application/json", ".png": "image/png", ".ico": "image/x-icon" }

async function serveDist(): Promise<{ server: Server; origin: string }> {
  const root = join(process.cwd(), "dist")
  const server = createServer((req, res) => {
    const path = decodeURIComponent((req.url ?? "/").split("?")[0]!)
    let file = join(root, path)
    if (!file.startsWith(root) || !existsSync(file) || !statSync(file).isFile()) file = join(root, "index.html")
    res.setHeader("Content-Security-Policy", CSP)
    res.setHeader("Content-Type", MIME[extname(file)] ?? "application/octet-stream")
    createReadStream(file).pipe(res)
  })
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r))
  const addr = server.address()
  return { server, origin: `http://127.0.0.1:${typeof addr === "object" && addr !== null ? addr.port : 0}` }
}

test("A5: no page or console reports a CSP violation under the controller's policy", async ({ page }) => {
  expect(existsSync(join("dist", "index.html")), "run pnpm -C web build first").toBe(true)
  const html = readFileSync(join("dist", "index.html"), "utf8")
  expect(html.match(/<script(?![^>]*\bsrc=)[^>]*>/g) ?? []).toEqual([])
  const { server, origin } = await serveDist()
  try {
    const problems: string[] = []
    page.on("console", (m) => {
      if (/content security policy|refused to/i.test(m.text())) problems.push(`console: ${m.text()}`)
    })
    page.on("pageerror", (e) => problems.push(`pageerror: ${e.message}`))
    await page.addInitScript(() => {
      ;(window as unknown as { __csp: string[] }).__csp = []
      document.addEventListener("securitypolicyviolation", (e) => (window as unknown as { __csp: string[] }).__csp.push(`${e.violatedDirective} ${e.blockedURI}`))
    })
    await mockApi(page, { signedIn: true })
    for (const path of ["/", `/hosts/${HOST_ID}`, system, `${system}/packages`, docker, "/audit"]) {
      await page.goto(origin + path)
      await page.waitForLoadState("networkidle")
      await expect(page.locator("h1, h2").first(), path).toBeVisible()
      problems.push(...(await page.evaluate(() => (window as unknown as { __csp: string[] }).__csp)).map((v) => `${path}: ${v}`))
    }
    expect(problems).toEqual([])
  } finally {
    server.close()
  }
})

// ---------------------------------------------------------------------------
// UI-3, UI-4: hosts list and host overview

test("A11: the hosts list shows a card per host with online state, memory and disk bars and module icons; the table toggle persists in the URL", async ({ page }) => {
  await open(page, "/")
  const online = page.getByRole("article", { name: HOST })
  await expect(online).toBeVisible()
  await expect(online.getByText("Online")).toBeVisible()
  await expect(online.getByRole("img", { name: /^Memory:/ })).toBeVisible()
  await expect(online.getByRole("img", { name: /^Disk:/ })).toBeVisible()
  await expect(online.getByRole("list", { name: "Modules" })).toContainText(/Host information[\s\S]*System[\s\S]*Firewall[\s\S]*Docker/)
  const offline = page.getByRole("article", { name: "db-1" })
  await expect(offline).toBeVisible()
  await expect(offline.getByText("Offline")).toBeVisible()
  await expect(page.getByRole("table")).toHaveCount(0)
  const before = page.url()
  await page.getByRole("radio", { name: "Table" }).click()
  await expect(page.getByRole("table")).toBeVisible()
  expect(page.url()).not.toBe(before)
  expect(new URL(page.url()).search).toMatch(/table/)
  await page.reload()
  await expect(page.getByRole("table")).toBeVisible()
  await expect(page.getByRole("radio", { name: "Table" })).toBeChecked()
  await page.getByRole("radio", { name: "Cards" }).click()
  await expect(page.getByRole("table")).toHaveCount(0)
})

test("A12: the host overview shows memory, disk and swap gauges, load per core, uptime and the four tiles", async ({ page }) => {
  await open(page, `/hosts/${HOST_ID}`)
  for (const gauge of [/^Memory:/, /^Disk:/, /^Swap:/]) await expect(page.getByRole("img", { name: gauge }).first(), String(gauge)).toBeVisible()
  await expect(page.getByText("Load per core")).toBeVisible()
  await expect(page.getByText("Uptime")).toBeVisible()
  for (const tile of ["Updates", "Docker", "Firewall", "Tailscale"]) await expect(page.getByText(tile, { exact: true }).first(), tile).toBeVisible()
})

test("A12: an offline host shows last seen instead of gauges", async ({ page }) => {
  await open(page, `/hosts/${OFFLINE_HOST_ID}`)
  await expect(page.getByText(/last seen|never seen/i).first()).toBeVisible()
  await expect(page.getByRole("img", { name: /^(Memory|Disk|Swap):/ })).toHaveCount(0)
})

// ---------------------------------------------------------------------------
// UI-8: ChangeDialog

test("A13: ChangeDialog shows plan, confirm and apply as a stepper and diff lines carry add, change or remove icons", async ({ page }) => {
  await open(page, `${system}/packages`)
  await page.getByRole("searchbox", { name: "Search packages" }).fill("htop")
  await page.getByRole("button", { name: "Install htop" }).click()
  let dialog = page.getByRole("dialog")
  const progress = dialog.getByRole("list", { name: "Progress" })
  await expect(progress.getByRole("listitem")).toHaveText([/Plan/, /Confirm/, /Apply/])
  await expect(dialog.getByRole("img", { name: /^(Add|Added|Create)/ }).first(), "icon on a + line").toBeVisible()
  await page.keyboard.press("Escape")
  await page.getByRole("button", { name: "Remove jq" }).click()
  dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("img", { name: /^(Remove|Delete)/ }).first(), "icon on a - line").toBeVisible()
  await page.keyboard.press("Escape")
  await page.goto(system)
  await page.getByRole("button", { name: "Refresh" }).first().click()
  dialog = page.getByRole("dialog")
  await expect(dialog.getByRole("img", { name: /^(Change|Modify|Update)/ }).first(), "icon on a ~ line").toBeVisible()
  await expect(dialog.getByRole("list", { name: "Progress" }).getByRole("listitem")).toHaveCount(3)
})

// ---------------------------------------------------------------------------
// UX-7, UX-8: state matrices

async function startDockerInstall(page: Page) {
  await page.getByRole("button", { name: "Install Docker" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Apply" }).click()
  await expect(dialog.getByRole("link", { name: "View change" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(dialog).toHaveCount(0)
}

const matrix: Record<string, { state: SystemState; path: string; check: (p: Page) => Promise<void> }> = {
  loading: {
    state: "loading",
    path: system,
    check: async (p) => {
      await expect(p.locator('[aria-busy="true"]').first()).toBeVisible()
    },
  },
  "offline host": {
    state: "offline",
    path: system,
    check: async (p) => {
      await expect(p.getByText("Host offline").first()).toBeVisible()
      await expect(p.getByText(/last seen/i).first()).toBeVisible()
      await expect(p.getByRole("button", { name: "Upgrade all" })).toBeDisabled()
      await expect(p.getByRole("button", { name: "Reboot" }).first()).toBeDisabled()
    },
  },
  "module disabled": {
    state: "disabled",
    path: system,
    check: async (p) => {
      await expect(p.getByText(/not enabled/i).first()).toBeVisible()
    },
  },
  "no updates": {
    state: "upToDate",
    path: system,
    check: async (p) => {
      await expect(p.getByText("Up to date")).toBeVisible()
    },
  },
  "apt busy": {
    state: "aptBusy",
    path: system,
    check: async (p) => {
      await expect(p.getByText("Another package manager is running")).toBeVisible()
      await expect(p.getByRole("button", { name: "Retry" }).or(p.getByRole("link", { name: "Retry" })).first()).toBeVisible()
      await expect(p.getByText("pending").first(), "tiles still read").toBeVisible()
    },
  },
  "failed sources": {
    state: "failedSources",
    path: system,
    check: async (p) => {
      await expect(p.getByText(/sources? failed/i)).toBeVisible()
      await expect(p.getByText(/deb\.example\.com/)).toBeVisible()
    },
  },
  "dpkg interrupted": {
    state: "dpkgInterrupted",
    path: system,
    check: async (p) => {
      await expect(p.getByRole("button", { name: "Repair packages" })).toBeVisible()
    },
  },
  "failed change": {
    state: "failedChange",
    path: system,
    check: async (p) => {
      await expect(p.getByText("libfoo1").first()).toBeVisible()
      await expect(p.getByText(/broken dependencies/i).first()).toBeVisible()
    },
  },
  "agent too old": {
    state: "agentTooOld",
    path: docker,
    check: async (p) => {
      await expect(p.getByText("Agent update needed")).toBeVisible()
    },
  },
  "tile error": {
    state: "error",
    path: system,
    check: async (p) => {
      await expect(p.getByRole("alert").first()).toBeVisible({ timeout: 15_000 })
      await expect(p.getByRole("button", { name: "Retry" }).first()).toBeVisible()
      await expect(p.getByText("Automatic updates")).toBeVisible()
    },
  },
  "docker absent": {
    state: "dockerAbsent",
    path: docker,
    check: async (p) => {
      await expect(p.getByRole("button", { name: "Install Docker" })).toBeVisible()
    },
  },
  "docker distro": {
    state: "dockerDistro",
    path: docker,
    check: async (p) => {
      await expect(p.getByText("docker.io")).toBeVisible()
      await expect(p.getByRole("link", { name: /replace/i })).toBeVisible()
      await expect(p.getByRole("button", { name: "Install Docker" })).toHaveCount(0)
    },
  },
  "docker stopped": {
    state: "dockerStopped",
    path: docker,
    check: async (p) => {
      await expect(p.getByText("Daemon stopped").first()).toBeVisible()
      await expect(p.getByRole("button", { name: /install|start|uninstall/i })).toHaveCount(0)
    },
  },
  installing: {
    state: "installing",
    path: docker,
    check: async (p) => {
      await startDockerInstall(p)
      await expect(p.getByRole("list", { name: "Progress" })).toBeVisible()
    },
  },
  "awaiting firewall confirmation": {
    state: "awaitingConfirm",
    path: docker,
    check: async (p) => {
      await startDockerInstall(p)
      await expect(p.getByRole("button", { name: "Keep" })).toBeVisible()
      await expect(p.getByRole("button", { name: "Revert" })).toBeVisible()
    },
  },
  "rolled back": {
    state: "rolledBack",
    path: docker,
    check: async (p) => {
      await startDockerInstall(p)
      await expect(p.getByText("Install rolled back")).toBeVisible()
      await expect(p.getByRole("button", { name: "Install Docker" })).toBeVisible()
    },
  },
  "install failed": {
    state: "installFailed",
    path: docker,
    check: async (p) => {
      await startDockerInstall(p)
      await expect(p.getByRole("button", { name: /retry|install docker/i }).first()).toBeVisible()
      await expect(p.getByText(/fail/i).first()).toBeVisible()
    },
  },
}

for (const vp of VIEWPORTS) {
  test(`A33 ${vp.width}x${vp.height}: every System overview and Docker install state renders as specified`, async ({ page }) => {
    await page.setViewportSize(vp)
    for (const [name, m] of Object.entries(matrix)) {
      await page.unrouteAll({ behavior: "ignoreErrors" })
      const fixtures = await mockApi(page, { signedIn: true, systemState: m.state })
      await page.goto(m.path)
      await m.check(page)
      expect(await noHorizontalScroll(page), `${name}: horizontal scroll`).toBe(true)
      expect(fixtures.unhandled, name).toEqual([])
    }
  })

  test(`A33 ${vp.width}x${vp.height}: the worst-case dataset renders`, async ({ page }) => {
    await page.setViewportSize(vp)
    await open(page, `${system}/updates`, { worstCase: true, systemState: undefined })
    await expect(page.getByText(/400/).first()).toBeVisible()
    expect(await noHorizontalScroll(page), "updates").toBe(true)
    await page.goto(`${system}/packages`)
    await expect(page.getByText(/3000/).first()).toBeVisible()
    expect(await noHorizontalScroll(page), "packages").toBe(true)
    await page.goto(system)
    await expect(page.getByRole("button", { name: "Upgrade all" })).toBeVisible()
    expect(await noHorizontalScroll(page), "overview").toBe(true)
  })
}

test("A33: the host name of 64 characters and the 50-host list keep the layout", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 })
  await open(page, "/", { worstCase: true })
  expect(await noHorizontalScroll(page)).toBe(true)
})

// ---------------------------------------------------------------------------
// A32, A50, A53, A54, A56

test("A32: after a successful install the Docker page shows its tabs without a page reload", async ({ page }) => {
  await open(page, docker, { systemState: "dockerAbsent" })
  await page.evaluate(() => ((window as unknown as { __qaMarker: number }).__qaMarker = 1))
  await expect(page.getByRole("link", { name: "Containers" })).toHaveCount(0)
  await page.getByRole("button", { name: "Install Docker" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
  await expect(page.getByRole("button", { name: "Keep" }).first()).toBeVisible({ timeout: 20_000 })
  await page.keyboard.press("Escape")
  await expect(page.getByRole("dialog")).toHaveCount(0)
  await page.getByRole("button", { name: "Keep" }).click()
  await expect(page.getByRole("link", { name: "Containers" }).or(page.getByRole("tab", { name: "Containers" })).first()).toBeVisible({ timeout: 20_000 })
  expect(await page.evaluate(() => (window as unknown as { __qaMarker?: number }).__qaMarker)).toBe(1)
})

test("A50: a host whose agent reports no system module shows Agent update needed with the update action on every System surface", async ({ page }) => {
  await mockApi(page, { signedIn: true, systemState: "agentTooOld" })
  for (const path of [system, `${system}/updates`, `${system}/packages`, docker, `/hosts/${HOST_ID}`]) {
    await page.goto(path)
    await expect(page.getByText("Agent update needed").first(), path).toBeVisible()
    await expect(page.getByRole("button", { name: /update agent/i }).or(page.getByRole("link", { name: /update agent/i })).first(), path).toBeVisible()
  }
})

test("A53: the Install unattended-upgrades button opens the install change for it", async ({ page }) => {
  await mockApi(page, { signedIn: true })
  await page.route(/\/resources\/system\/autoupdates$/, (route) => route.fulfill({ json: { installed: false, enabled: false } }))
  await page.goto(system)
  await page.getByRole("button", { name: "Install unattended-upgrades" }).click()
  const dialog = page.getByRole("dialog")
  await expect(dialog).toContainText("unattended-upgrades")
  await expect(dialog.getByRole("list", { name: "Progress" })).toBeVisible()
})

test("A54: a protected row shows a lock with its reason in a tip and has no remove button", async ({ page }) => {
  await open(page, `${system}/packages`)
  await expect(page.getByRole("button", { name: "Remove bash" })).toHaveCount(0)
  await expect(page.getByRole("button", { name: "Remove sudo" })).toHaveCount(0)
  await page.getByRole("button", { name: "Why bash is protected" }).focus()
  await expect(page.getByText("Removing it would break the system").first()).toBeVisible()
  await expect(page.getByRole("button", { name: "Remove jq" })).toBeVisible()
})

test("A56: a refresh with a failing source names it as a warning on the updates tile", async ({ page }) => {
  await open(page, system, { systemState: "failedSources" })
  await expect(page.getByText(/2 sources failed/)).toBeVisible()
  await expect(page.getByText("http://deb.example.com/debian bookworm InRelease")).toBeVisible()
  await expect(page.getByRole("button", { name: "Refresh" }).first()).toBeEnabled()
})

// ---------------------------------------------------------------------------
// A24, A41: reboot states derive from the change output, the boot time and the
// connection, and survive a reload

const NOW = new Date("2026-11-20T12:00:00Z")
const BOOT_BEFORE = "2026-09-25T10:00:00Z"
const SUMMARY = {
  hostname: "web-1",
  os: { id: "debian", versionId: "12", prettyName: "Debian GNU/Linux 12 (bookworm)" },
  kernel: "6.1.0-26-amd64",
  arch: "amd64",
  uptimeSeconds: 3_553_200,
  bootTime: BOOT_BEFORE,
  cpu: { model: "AMD EPYC 7763", logicalCount: 4 },
  memory: { totalBytes: 8 * 2 ** 30, availableBytes: 1.8 * 2 ** 30 },
  swap: { totalBytes: 2 * 2 ** 30, freeBytes: 1.6 * 2 ** 30 },
  load: { one: 0.42, five: 0.37, fifteen: 0.31 },
  rootFs: { totalBytes: 100 * 2 ** 30, availableBytes: 59 * 2 ** 30 },
  darkpanelVersion: "1.0.0",
}

async function rebootPage(page: Page, opts: { scheduledSecondsAgo: number; online: boolean; bootTime: string }) {
  await page.clock.setFixedTime(NOW)
  await mockApi(page, { signedIn: true, systemState: opts.online ? undefined : "offline" })
  const at = new Date(NOW.getTime() - opts.scheduledSecondsAgo * 1000).toISOString()
  const change = {
    id: "qa-reboot-1", hostId: HOST_ID, action: "system.host.reboot", version: 1, class: "irreversible", state: "completed", actor: "user:admin",
    createdAt: at, updatedAt: at, revertDeadline: null, output: { scheduledAt: at, bootTimeBefore: BOOT_BEFORE }, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
  await page.route(/\/api\/v1\/changes\?/, (route) => route.fulfill({ json: { changes: [change] } }))
  await page.route(/\/resources\/hostinfo\/summary$/, (route) => route.fulfill({ json: { ...SUMMARY, bootTime: opts.bootTime } }))
  await page.goto(system)
}

test("A24: the reboot tile shows Rebooting once the host drops and Back online with a new boot time", async ({ page }) => {
  await rebootPage(page, { scheduledSecondsAgo: 20, online: false, bootTime: BOOT_BEFORE })
  await expect(page.getByText("Rebooting").first()).toBeVisible()
  await page.unrouteAll({ behavior: "ignoreErrors" })
  await rebootPage(page, { scheduledSecondsAgo: 120, online: true, bootTime: new Date(NOW.getTime() - 30_000).toISOString() })
  await expect(page.getByText("Back online").first()).toBeVisible()
})

test("A41: Reboot did not start, Rebooting and Not back after 10 minutes each survive a reload", async ({ page }) => {
  const cases = [
    { label: "Reboot did not start", opts: { scheduledSecondsAgo: 180, online: true, bootTime: BOOT_BEFORE } },
    { label: "Rebooting", opts: { scheduledSecondsAgo: 60, online: false, bootTime: BOOT_BEFORE } },
    { label: "Not back after 10 minutes", opts: { scheduledSecondsAgo: 700, online: false, bootTime: BOOT_BEFORE } },
  ]
  for (const c of cases) {
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await rebootPage(page, c.opts)
    await expect(page.getByText(c.label).first(), c.label).toBeVisible()
    await page.reload()
    await expect(page.getByText(c.label).first(), `${c.label} after a reload`).toBeVisible()
  }
})

// ---------------------------------------------------------------------------
// A51: 50 hosts whose agents answer after 8 seconds

test("A51: the hosts list renders every card at once, keeps at most 6 summary reads in flight and shows the error icon after 5 seconds", async ({ page }) => {
  await mockApi(page, { signedIn: true, worstCase: true })
  // Every read lasts at least the client's 5-second timeout, so the reads in
  // flight at any time are the ones started in the last 5 seconds.
  const starts: number[] = []
  const summary = /\/resources\/hostinfo\/summary$/
  page.on("request", (r) => {
    if (summary.test(new URL(r.url()).pathname)) starts.push(Date.now())
  })
  await page.route(summary, async (route) => {
    await new Promise((r) => setTimeout(r, 8000))
    await route.fallback().catch(() => undefined)
  })
  const t0 = Date.now()
  await page.goto("/")
  await expect(page.getByRole("article")).toHaveCount(50, { timeout: 4000 })
  expect(Date.now() - t0, "every card frame renders before any read answers").toBeLessThan(8000)
  await page.waitForTimeout(7000)
  const peak = Math.max(0, ...starts.map((s) => starts.filter((t) => t >= s && t < s + 4500).length))
  expect(peak, "summary reads in flight").toBeLessThanOrEqual(6)
  expect(peak, "reads were issued").toBeGreaterThan(0)
  const errors = page.getByRole("article").getByRole("img", { name: /unavailable/i })
  await expect(errors.first()).toBeVisible()
  const onlineCards = await page.getByRole("article").filter({ hasText: "Online" }).count()
  expect(onlineCards).toBeGreaterThan(0)
})

// ---------------------------------------------------------------------------
// A34, A35, A36

for (const theme of ["light", "dark"] as const) {
  test(`A34: axe reports no violation on every route in the ${theme} theme`, async ({ page }) => {
    test.setTimeout(240_000)
    await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
    const bad: string[] = []
    for (const r of ROUTES) {
      await page.unrouteAll({ behavior: "ignoreErrors" })
      await open(page, r.path, { signedIn: r.signedIn })
      await expect(page.locator("html")).toHaveAttribute("data-mode", theme)
      await page.addScriptTag({ path: axePath })
      const found = await page.evaluate(async () => {
        const axe = (window as unknown as { axe: { run: (c: Document, o: object) => Promise<{ violations: { id: string; nodes: { target: string[] }[] }[] }> } }).axe
        const res = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] })
        return res.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)
      })
      for (const f of found) bad.push(`${r.name} (${theme}): ${f}`)
    }
    expect(bad).toEqual([])
  })
}

async function tabTo(page: Page, role: string, name: string | RegExp) {
  for (const deadline = Date.now() + 15_000; Date.now() < deadline; ) {
    await page.keyboard.press("Tab")
    const label = await page.evaluate(() => {
      const el = document.activeElement as HTMLInputElement | null
      if (el === null || el === document.body) return null
      const role = el.getAttribute("role") ?? (el.tagName === "BUTTON" ? "button" : el.tagName === "A" ? "link" : el.tagName === "INPUT" ? (el.type === "checkbox" ? "checkbox" : el.type === "search" ? "searchbox" : "textbox") : "")
      const text = el.getAttribute("aria-label") ?? el.labels?.[0]?.textContent?.trim() ?? el.textContent?.trim() ?? ""
      return `${role}|${text}`
    })
    if (label !== null) {
      const [r, ...rest] = label.split("|")
      const n = rest.join("|")
      if (r === role && (typeof name === "string" ? n === name : name.test(n))) return
    }
    await page.waitForTimeout(15)
  }
  throw new Error(`no ${role} "${String(name)}" reachable by Tab`)
}

const flows: { name: string; state?: SystemState; route: string; steps: (p: Page) => Promise<void>; after?: (p: Page) => Promise<void> }[] = [
  {
    name: "UX-1 upgrade a host",
    route: system,
    steps: async (p) => {
      await tabTo(p, "button", "Upgrade all")
      await p.keyboard.press("Enter")
      await tabTo(p, "textbox", /./)
      await p.keyboard.type(HOST)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByText("Up to date")).toBeVisible({ timeout: 15_000 })
    },
  },
  {
    name: "UX-2 install a package",
    route: `${system}/packages`,
    steps: async (p) => {
      await tabTo(p, "searchbox", "Search packages")
      await p.keyboard.type("htop")
      await expect(p.getByRole("button", { name: "Install htop" })).toBeVisible()
      await tabTo(p, "button", "Install htop")
      await p.keyboard.press("Enter")
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByRole("button", { name: "Remove htop" })).toBeVisible({ timeout: 15_000 })
    },
  },
  {
    name: "UX-3 remove a package",
    route: `${system}/packages`,
    steps: async (p) => {
      await tabTo(p, "button", "Remove jq")
      await p.keyboard.press("Enter")
      await tabTo(p, "textbox", /./)
      await p.keyboard.type(HOST)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByRole("button", { name: "Remove jq" })).toHaveCount(0, { timeout: 15_000 })
    },
  },
  {
    name: "UX-4 reboot",
    route: system,
    steps: async (p) => {
      await tabTo(p, "button", "Reboot")
      await p.keyboard.press("Enter")
      await tabTo(p, "textbox", /./)
      await p.keyboard.type(HOST)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByText(/Reboot scheduled|Rebooting/).first()).toBeVisible({ timeout: 15_000 })
    },
  },
  {
    name: "UX-5 install Docker",
    state: "dockerAbsent",
    route: docker,
    steps: async (p) => {
      await tabTo(p, "button", "Install Docker")
      await p.keyboard.press("Enter")
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
  },
  {
    name: "UX-6 uninstall Docker",
    route: system,
    steps: async (p) => {
      await tabTo(p, "button", "Uninstall")
      await p.keyboard.press("Enter")
      await tabTo(p, "checkbox", /Delete all containers/)
      await p.keyboard.press("Space")
      await tabTo(p, "button", "Uninstall")
      await p.keyboard.press("Enter")
      await tabTo(p, "textbox", /./)
      await p.keyboard.type(`purge docker on ${HOST}`)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
  },
]

for (const flow of flows) {
  test(`A34: ${flow.name} completes by keyboard alone`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const fixtures = await open(page, flow.route, { systemState: flow.state ?? "default" })
    await flow.steps(page)
    await expect(page.getByRole("link", { name: "View change" }).first()).toBeVisible({ timeout: 15_000 })
    await page.keyboard.press("Escape")
    await expect(page.getByRole("dialog")).toHaveCount(0)
    await flow.after?.(page)
    expect(fixtures.unhandled).toEqual([])
  })
}

test("A35: no route scrolls horizontally at 375x812", async ({ page }) => {
  test.setTimeout(180_000)
  await page.setViewportSize({ width: 375, height: 812 })
  const wide: string[] = []
  for (const r of ROUTES) {
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await open(page, r.path, { signedIn: r.signedIn })
    if (!(await noHorizontalScroll(page))) wide.push(r.name)
  }
  for (const state of ["dockerAbsent", "failedSources", "aptBusy", "failedChange"] as SystemState[]) {
    await page.unrouteAll({ behavior: "ignoreErrors" })
    await open(page, state === "dockerAbsent" ? docker : system, { systemState: state })
    if (!(await noHorizontalScroll(page))) wide.push(state)
  }
  expect(wide).toEqual([])
})

for (const [name, path] of [
  ["/", "/"],
  ["the host overview", `/hosts/${HOST_ID}`],
] as const) {
  test(`A36: LCP under 2.5 s and CLS under 0.1 on ${name}`, async ({ page }) => {
    await page.addInitScript(() => {
      const w = window as unknown as { __lcp: number; __cls: number }
      w.__lcp = 0
      w.__cls = 0
      new PerformanceObserver((list) => {
        for (const e of list.getEntries()) w.__lcp = e.startTime
      }).observe({ type: "largest-contentful-paint", buffered: true })
      new PerformanceObserver((list) => {
        for (const e of list.getEntries() as unknown as { hadRecentInput: boolean; value: number }[]) if (!e.hadRecentInput) w.__cls += e.value
      }).observe({ type: "layout-shift", buffered: true })
    })
    await open(page, path)
    await page.waitForTimeout(2500)
    const { lcp, cls } = await page.evaluate(() => {
      const w = window as unknown as { __lcp: number; __cls: number }
      return { lcp: w.__lcp, cls: w.__cls }
    })
    expect(lcp, "LCP ms").toBeGreaterThan(0)
    expect(lcp, "LCP ms").toBeLessThan(2500)
    expect(cls, "CLS").toBeLessThan(0.1)
  })
}
