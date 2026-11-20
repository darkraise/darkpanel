import { expect, test, type Page } from "@playwright/test"
import { HOST_ID, INSTALL_STATES, SYSTEM_STATES, mockApi, type SystemState } from "./fixtures"

test.use({ baseURL: `http://127.0.0.1:${process.env.E2E_PORT ?? "0"}` })

const HOST = "web-1"
const system = `/hosts/${HOST_ID}/system`
const docker = `/hosts/${HOST_ID}/docker`
const viewports = [
  { width: 375, height: 812 },
  { width: 768, height: 1024 },
  { width: 1440, height: 900 },
]

function routeOf(state: SystemState): string {
  return state.startsWith("docker") || INSTALL_STATES.includes(state) ? docker : system
}

// The install panel learns of an install only from its own apply, so the
// in-flight states start one: the fixtures answer the apply in that state.
async function startInstall(page: Page) {
  await page.getByRole("button", { name: "Install Docker" }).click()
  const dialog = page.getByRole("dialog")
  await dialog.getByRole("button", { name: "Apply" }).click()
  await expect(dialog.getByRole("link", { name: "View change" })).toBeVisible()
  await page.keyboard.press("Escape")
  await expect(dialog).toHaveCount(0)
}

for (const vp of viewports) {
  test(`A33 ${vp.width}: every System overview and Docker install state renders`, async ({ page }) => {
    await page.setViewportSize(vp)
    for (const state of SYSTEM_STATES.filter((s) => s !== "default")) {
      await page.unrouteAll({ behavior: "ignoreErrors" })
      const fixtures = await mockApi(page, { signedIn: true, systemState: state })
      await page.goto(routeOf(state))
      if (INSTALL_STATES.includes(state)) await startInstall(page)
      await expect(page.getByTestId(`state-${state}`).first(), state).toBeVisible()
      if (state === "agentTooOld") {
        await page.goto(docker)
        await expect(page.getByTestId("state-agentTooOld").first(), "agentTooOld on the Docker page").toBeVisible()
      }
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth), state).toBe(true)
      expect(fixtures.unhandled, state).toEqual([])
    }
  })
}

// tabTo presses Tab until the focused element has this role and name, for
// up to 10 seconds: a dialog's controls appear once its plan has answered.
async function tabTo(page: Page, role: string, name: string | RegExp) {
  const wanted = typeof name === "string" ? { text: name, regex: false } : { text: name.source, regex: true, flags: name.flags }
  for (const deadline = Date.now() + 10_000; Date.now() < deadline; ) {
    await page.keyboard.press("Tab")
    const hit = await page.evaluate(
      ([r, n]) => {
        const el = document.activeElement as HTMLInputElement | null
        if (el === null) return false
        const label = el.getAttribute("aria-label") ?? el.labels?.[0]?.textContent?.trim() ?? el.textContent?.trim() ?? ""
        const native: Record<string, string> = { BUTTON: "button", A: "link" }
        const input = el.type === "checkbox" ? "checkbox" : el.type === "search" ? "searchbox" : "textbox"
        const roleOf = el.getAttribute("role") ?? (el.tagName === "INPUT" ? input : native[el.tagName])
        return roleOf === r && (n.regex ? new RegExp(n.text, n.flags).test(label) : label === n.text)
      },
      [role, wanted] as const,
    )
    if (hit) return
    await page.waitForTimeout(20)
  }
  throw new Error(`no ${role} "${String(name)}" reachable by Tab`)
}

const flows: { name: string; state: SystemState; route: string; steps: (p: Page) => Promise<void>; after?: (p: Page) => Promise<void> }[] = [
  {
    name: "UX-1 upgrade all",
    state: "default",
    route: system,
    steps: async (p) => {
      await tabTo(p, "button", "Upgrade all")
      await p.keyboard.press("Enter")
      await tabTo(p, "textbox", /confirm/i)
      await p.keyboard.type(HOST)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByRole("region", { name: "Updates" }).getByText("Up to date")).toBeVisible()
    },
  },
  {
    // jq is installed in the default dataset (UX-3 removes it), so the
    // install flow installs htop.
    name: "UX-2 install a package",
    state: "default",
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
      await expect(p.getByRole("button", { name: "Remove htop" })).toBeVisible()
    },
  },
  {
    name: "UX-3 remove a package",
    state: "default",
    route: `${system}/packages`,
    steps: async (p) => {
      await tabTo(p, "button", "Remove jq")
      await p.keyboard.press("Enter")
      await tabTo(p, "textbox", /confirm/i)
      await p.keyboard.type(HOST)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByRole("button", { name: "Remove jq" })).toHaveCount(0)
    },
  },
  {
    name: "UX-4 reboot",
    state: "default",
    route: system,
    steps: async (p) => {
      await tabTo(p, "button", "Reboot")
      await p.keyboard.press("Enter")
      await tabTo(p, "textbox", /confirm/i)
      await p.keyboard.type(HOST)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByRole("region", { name: "Reboot" }).getByText("Reboot scheduled")).toBeVisible()
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
      await tabTo(p, "button", "Keep")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByRole("tab", { name: "Containers" }).or(p.getByRole("link", { name: "Containers" })).first()).toBeVisible({ timeout: 15_000 })
    },
  },
  {
    name: "UX-6 uninstall Docker",
    state: "default",
    route: system,
    steps: async (p) => {
      await tabTo(p, "button", "Uninstall")
      await p.keyboard.press("Enter")
      await tabTo(p, "checkbox", "Delete all containers, images and volumes")
      await p.keyboard.press("Space")
      const plan = p.waitForRequest((r) => r.url().includes("/system.docker.uninstall/v1/plan"))
      await tabTo(p, "button", "Uninstall")
      await p.keyboard.press("Enter")
      expect(((await plan).postDataJSON() as { input: { purge: boolean } }).input.purge).toBe(true)
      await tabTo(p, "textbox", /confirm/i)
      await p.keyboard.type(`purge docker on ${HOST}`)
      await tabTo(p, "button", "Apply")
      await p.keyboard.press("Enter")
    },
    after: async (p) => {
      await expect(p.getByRole("region", { name: "Docker Engine" }).getByText("Not installed")).toBeVisible()
    },
  },
]

for (const flow of flows) {
  test(`A34: ${flow.name} completes by keyboard alone`, async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const fixtures = await mockApi(page, { signedIn: true, systemState: flow.state })
    await page.goto(flow.route)
    await page.waitForLoadState("networkidle")
    await flow.steps(page)
    await expect(page.getByTestId("change-done")).toBeVisible()
    await page.keyboard.press("Escape")
    await expect(page.getByRole("dialog")).toHaveCount(0)
    await flow.after?.(page)
    expect(fixtures.unhandled).toEqual([])
  })
}
