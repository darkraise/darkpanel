import { createRequire } from "node:module"
import { expect, test, type Page, type Route } from "@playwright/test"
import { npInstance } from "../src/modules/npm/fixtures"
import { HOST_ID, mockApi } from "./fixtures"

const axePath = createRequire(createRequire(import.meta.url).resolve("vitest-axe")).resolve("axe-core/axe.min.js")
const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/w12c-deploy-form"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

const ON = "The Docker firewall integration is on: ports 80 and 443 are opened for the NPM container."
const OFF_FW =
  "The Docker firewall integration is off: the deploy allows ports 80 and 443 in the firewall as its last step. Then check them from outside and confirm that the instance is public."
const OFF = "The Docker firewall integration is off: open ports 80 and 443 to the internet yourself, then confirm that the instance is public."
const EMAIL = "letsencrypt-operations-team@infrastructure.example.com"

const READ = { docker: /\/resources\/firewall\/docker$/, status: /\/resources\/firewall\/status$/ } as const
type Read = keyof typeof READ

const NOT_DEPLOYED = npInstance({ deployed: false, containerId: "", image: "", running: false, apiReady: false, public: "no", adminEmail: "" })

const DOCKER = {
  available: true, version: "29.9.1", driver: "iptables", dockerVariant: "nft", ufwVariant: "nft", rawProtection: true, supported: true,
  enabled: true, health: "ok", networks: [], settings: { privateSources: [], forwardExemptions: [] }, rules: [], bindings: [],
}
const STATUS = {
  supported: true, unsupported: [], active: true, inSync: true, outOfSync: [],
  configured: { enabled: true, logLevel: "low", ipv6: true, defaults: { incoming: "DROP", outgoing: "ACCEPT", routed: "DROP" } },
  loaded: { variant: "nft", ipv6: true, sysctl: {}, chains: [] }, rules: [],
}

const json = (body: unknown, status = 200) => ({ status, contentType: "application/json", body: JSON.stringify(body) })

function watchConsole(page: Page): string[] {
  const errors: string[] = []
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push(m.text())
  })
  page.on("pageerror", (e) => errors.push(String(e)))
  return errors
}

// axe runs once the finite animations have finished, so it never samples the
// half-transparent dialog of a fade-in.
async function expectNoAxeViolations(page: Page, where: string) {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((animation) => animation.effect?.getComputedTiming().iterations !== Infinity)
        .map((animation) => animation.finished.catch(() => undefined)),
    ),
  )
  await page.addScriptTag({ path: axePath })
  const violations = await page.evaluate(async () => {
    const axe = (window as unknown as { axe: { run: (context: Document, options: object) => Promise<{ violations: { id: string; nodes: { target: string[] }[] }[] }> } }).axe
    const result = await axe.run(document, { runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] }, resultTypes: ["violations"] })
    return result.violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`)
  })
  expect(violations, where).toEqual([])
}

async function expectNoSidewaysScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), "the page does not scroll sideways").toBe(true)
}

interface Setup {
  docker?: Record<string, unknown>
  status?: Record<string, unknown>
  hold?: Read // this read never answers until the test releases it
  fail?: Read // this read answers 503
}

// open serves the NPM page of a host without NPM. Its routes are added after
// mockApi's, so they answer first.
async function open(page: Page, theme: (typeof THEMES)[number], setup: Setup) {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  const fixtures = await mockApi(page, { signedIn: true })
  await page.route(/\/api\/v1\/events$/, (route) => route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 200\n\n" }))
  await page.route(/\/actions\/npm\.instance\.status\/v1\/read$/, (route) => route.fulfill(json({ output: NOT_DEPLOYED })))
  let release = () => {}
  const held = new Promise<void>((resolve) => (release = resolve))
  const bodies: Record<Read, Record<string, unknown>> = { docker: { ...DOCKER, ...setup.docker }, status: { ...STATUS, ...setup.status } }
  for (const read of ["docker", "status"] as const) {
    await page.route(READ[read], async (route: Route) => {
      if (setup.hold === read) await held
      if (setup.fail === read) {
        const detail = `the firewall ${read} cannot be read`
        await route.fulfill(json({ type: "about:blank", title: "Unavailable", status: 503, detail, code: "module.unavailable" }, 503)).catch(() => {})
        return
      }
      await route.fulfill(json(bodies[read])).catch(() => {})
    })
  }
  const plans: { path: string; body: unknown }[] = []
  await page.route(/\/api\/v1\/actions\/npm\.instance\.deploy-public\/v\d+\/plan$/, async (route) => {
    plans.push({ path: new URL(route.request().url()).pathname, body: route.request().postDataJSON() })
    await route.fulfill(
      json({
        planId: "plan-deploy", hostId: null, action: "npm.instance.deploy-public", version: 2, inputHash: "sha256:fixture",
        diff: [{ op: "+", text: "container darkpanel-npm (jc21/nginx-proxy-manager)" }, { op: "+", text: "rule allow in tcp 443 from any (npm)" }],
        guards: [], expiresAt: "2099-01-01T00:00:00Z", confirmation: null,
      }),
    )
  })
  await page.goto(url(`/hosts/${HOST_ID}/npm`))
  return { unhandled: fixtures.unhandled, plans, release }
}

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    test.describe(tag, () => {
      test.use({ viewport: vp })

      const planned = [
        { id: "UI-1", name: "integration on", setup: {}, hint: ON, input: { integration: true, firewall: true, ipv6: true } },
        { id: "UI-2", name: "integration off, firewall managed", setup: { docker: { enabled: false } }, hint: OFF_FW, input: { integration: false, firewall: true, ipv6: true } },
        {
          id: "UI-3",
          name: "integration off, firewall not managed",
          setup: { docker: { enabled: false }, status: { supported: false, unsupported: ["rules"] } },
          hint: OFF,
          input: { integration: false, firewall: false, ipv6: true },
        },
      ]
      for (const c of planned) {
        test(`${c.id}, UX-1: ${c.name} ${tag}`, async ({ page }) => {
          const errors = watchConsole(page)
          const { unhandled, plans } = await open(page, theme, c.setup)
          const form = page.getByRole("form", { name: "Deploy" })
          await expect(form.getByText(c.hint, { exact: true })).toBeVisible()
          const plan = form.getByRole("button", { name: "Plan" })
          await expect(plan).toBeDisabled()
          await expectNoSidewaysScroll(page)
          await expectNoAxeViolations(page, `${c.id} form`)
          await page.screenshot({ path: `${EVIDENCE}/${c.id}-${tag}.png`, fullPage: true })

          await form.getByLabel("Let's Encrypt email").focus()
          await page.keyboard.type(EMAIL)
          await page.keyboard.press("Tab")
          await expect(plan).toBeFocused()
          await page.keyboard.press("Enter")
          const dialog = page.getByRole("dialog", { name: "Deploy Nginx Proxy Manager" })
          await expect(dialog.getByLabel("Planned changes")).toBeVisible()
          expect(plans, "UX-1 one plan of deploy-public v2 with what the form read").toEqual([
            { path: "/api/v1/actions/npm.instance.deploy-public/v2/plan", body: { input: { hostId: HOST_ID, acmeEmail: EMAIL, ...c.input } } },
          ])
          await expectNoAxeViolations(page, `${c.id} dialog`)
          await page.screenshot({ path: `${EVIDENCE}/UX-1-${c.id}-${tag}.png` })
          expect(errors, "console errors").toEqual([])
          expect(unhandled, "API requests without a fixture").toEqual([])
        })
      }

      for (const read of ["docker", "status"] as const) {
        test(`UI-4: ${read} read loading ${tag}`, async ({ page }) => {
          const errors = watchConsole(page)
          const { unhandled, release } = await open(page, theme, { hold: read })
          const form = page.getByRole("form", { name: "Deploy" })
          await expect(form.getByLabel("Loading the firewall state")).toBeVisible()
          await form.getByLabel("Let's Encrypt email").fill(EMAIL)
          await expect(form.getByRole("button", { name: "Plan" })).toBeDisabled()
          for (const hint of [ON, OFF_FW, OFF]) await expect(form.getByText(hint, { exact: true })).toHaveCount(0)
          await expectNoSidewaysScroll(page)
          await expectNoAxeViolations(page, `UI-4 ${read} loading`)
          await page.screenshot({ path: `${EVIDENCE}/UI-4-${tag}-${read}-loading.png`, fullPage: true })
          release()
          await expect(form.getByText(ON, { exact: true })).toBeVisible()
          await expect(form.getByLabel("Loading the firewall state")).toHaveCount(0)
          await expect(form.getByRole("button", { name: "Plan" })).toBeEnabled()
          expect(errors, "console errors").toEqual([])
          expect(unhandled, "API requests without a fixture").toEqual([])
        })

        test(`UI-5: ${read} read failed ${tag}`, async ({ page }) => {
          const errors = watchConsole(page)
          const { unhandled } = await open(page, theme, { fail: read })
          const form = page.getByRole("form", { name: "Deploy" })
          await expect(form.getByRole("alert")).toHaveText(`the firewall ${read} cannot be read`, { timeout: 15_000 })
          await form.getByLabel("Let's Encrypt email").fill(EMAIL)
          await expect(form.getByRole("button", { name: "Plan" })).toBeDisabled()
          for (const hint of [ON, OFF_FW, OFF]) await expect(form.getByText(hint, { exact: true })).toHaveCount(0)
          await expectNoSidewaysScroll(page)
          await expectNoAxeViolations(page, `UI-5 ${read} failed`)
          await page.screenshot({ path: `${EVIDENCE}/UI-5-${tag}-${read}-failed.png`, fullPage: true })
          expect(errors, "console errors").toEqual([])
          expect(unhandled, "API requests without a fixture").toEqual([])
        })
      }
    })
  }
}
