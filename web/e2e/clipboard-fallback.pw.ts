import { expect, test, type Locator, type Page } from "@playwright/test"
import { PENDING_HOST_ID, mockApi } from "./fixtures"

const VIEWPORTS = [{ width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1440, height: 900 }] as const
const THEMES = ["light", "dark"] as const
const EVIDENCE = "../.dforge/ui-ux/public-http-listener"
const NOW = "2026-11-19T10:00:00.000Z"
const LATER = "2026-12-19T10:00:00.000Z"
const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

// answerActions answers the named actions on top of mockApi, which has no
// fixtures for the token and tailnet-key dialogs.
async function answerActions(page: Page, answers: Record<string, { read?: unknown; apply?: unknown }>) {
  await page.route(
    (u) => u.pathname.startsWith("/api/v1/actions/"),
    async (route) => {
      const m = /^\/api\/v1\/actions\/([^/]+)\/v1\/(read|plan|apply)$/.exec(new URL(route.request().url()).pathname)
      const action = m?.[1] ?? ""
      const found = m === null ? undefined : answers[action]
      if (m === null || found === undefined) {
        await route.fallback()
        return
      }
      const json =
        m[2] === "read"
          ? { output: found.read }
          : m[2] === "plan"
            ? { planId: `plan-${action}`, hostId: null, action, version: 1, inputHash: "h", diff: [{ op: "+", text: action }], guards: [], expiresAt: LATER, confirmation: null }
            : {
                id: `change-${action}`, hostId: null, action, version: 1, class: "irreversible", state: "completed", actor: "admin", createdAt: NOW,
                updatedAt: NOW, revertDeadline: null, output: found.apply, error: null, compensatedBy: null, parentChangeId: null, owner: null,
                repairedBy: null, repairOutcome: null, override: null,
              }
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(json) })
    },
  )
}

const DIALOGS: Array<{ name: string; secret: string; open: (page: Page) => Promise<Locator> }> = [
  {
    name: "enrollment-token",
    secret: "dpe_fixture_token",
    open: async (page) => {
      await page.goto(url(`/hosts/${PENDING_HOST_ID}`))
      await page.getByRole("button", { name: "New enrollment token" }).click()
      await page.getByLabel("Controller address (optional)").fill("panel.example.com:7443")
      await page.keyboard.press("Enter")
      const change = page.getByRole("dialog")
      await change.getByLabel("Type new-1 to confirm").fill("new-1")
      await change.getByRole("button", { name: "Apply" }).click()
      return page.getByLabel("Enrollment token for new-1")
    },
  },
  {
    name: "api-token",
    secret: "dpt_proof_secret_0123456789",
    open: async (page) => {
      await answerActions(page, {
        "core.token.list": { read: { tokens: [] } },
        "core.token.create": {
          apply: { id: "01JTOKEN", name: "ci", scope: "read", createdAt: NOW, expiresAt: LATER, lastUsedAt: null, revokedAt: null, token: "dpt_proof_secret_0123456789" },
        },
      })
      await page.goto(url("/settings/tokens"))
      await page.getByRole("button", { name: "Create token" }).first().click()
      await page.getByLabel("Name").fill("ci")
      await page.getByRole("dialog").getByRole("button", { name: "Create" }).click()
      return page.getByLabel("Token ci")
    },
  },
  {
    name: "tailnet-key",
    secret: "tskey-auth-proof-0123456789",
    open: async (page) => {
      await answerActions(page, {
        "tailnet.account.get": {
          read: { connected: true, account: { tailnet: "example.ts.net", clientId: "client-1", tagsAllowed: ["tag:server"], capabilities: [], connectedAt: NOW } },
        },
        "tailnet.authkey.list": { read: { keys: [] } },
        "tailnet.authkey.create": { apply: { id: "k1", key: "tskey-auth-proof-0123456789", expires: LATER, description: "proof" } },
      })
      await page.goto(url("/tailnet/keys"))
      await page.getByLabel("tag:server").check()
      await page.getByRole("button", { name: "Create key" }).click()
      await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
      return page.getByLabel("Auth key proof")
    },
  },
]

// The harness serves 127.0.0.1, which browsers treat as a secure context, so
// the clipboard API is removed before the app loads.
async function setUp(page: Page, theme: string, copies: boolean) {
  await page.addInitScript((mode) => localStorage.setItem("mode", mode), theme)
  await page.addInitScript((ok) => {
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true })
    if (!ok) document.execCommand = () => false
    document.addEventListener("copy", () => {
      const el = document.activeElement
      if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
        document.documentElement.dataset.copied = el.value.slice(el.selectionStart ?? 0, el.selectionEnd ?? 0)
      }
    })
  }, copies)
  return mockApi(page, { signedIn: true, worstCase: true })
}

for (const theme of THEMES) {
  for (const vp of VIEWPORTS) {
    const tag = `${vp.width}x${vp.height}-${theme}`
    for (const dialog of DIALOGS) {
      test(`UX-4: ${dialog.name} Copy works without the clipboard API ${tag}`, async ({ page }) => {
        await page.setViewportSize(vp)
        const fixtures = await setUp(page, theme, true)
        const field = await dialog.open(page)
        await expect(field).toHaveValue(dialog.secret)
        await expect(page.getByRole("dialog")).toHaveCount(1)
        const copy = page.getByRole("dialog").getByRole("button", { name: /^Copy/ })
        await copy.click()
        await expect(copy).toHaveAttribute("data-copied", "true")
        await expect(page.locator("html")).toHaveAttribute("data-copied", dialog.secret)
        await expect(copy).toBeFocused()
        await page.mouse.move(0, 0)
        await page.screenshot({ path: `${EVIDENCE}/UX-4-${tag}-${dialog.name}.png` })
        expect(fixtures.unhandled).toEqual([])
      })

      test(`UX-4: ${dialog.name} a failed copy says so ${tag}`, async ({ page }) => {
        await page.setViewportSize(vp)
        const fixtures = await setUp(page, theme, false)
        const field = await dialog.open(page)
        await expect(field).toHaveValue(dialog.secret)
        await expect(page.getByRole("dialog")).toHaveCount(1)
        await page.getByRole("dialog").getByRole("button", { name: /^Copy/ }).click()
        await expect(page.getByRole("dialog").getByRole("alert")).toHaveText("Copy failed. Select the text and copy it yourself.")
        await expect(field).toBeEditable({ editable: false })
        await page.mouse.move(0, 0)
        await page.screenshot({ path: `${EVIDENCE}/UX-4-${tag}-${dialog.name}-failed.png` })
        expect(fixtures.unhandled).toEqual([])
      })
    }
  }
}
