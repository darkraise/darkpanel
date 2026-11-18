import { expect, test, type Locator, type Page } from "@playwright/test"
import { PENDING_HOST_ID, mockApi } from "./fixtures"

// A13 (UX-4): Copy in the three show-once dialogs on an origin without
// navigator.clipboard. The page runs the real app against the fixture API; the
// init script removes (or replaces) the clipboard API and decides what
// document.execCommand("copy") does, recording the text that was selected.

type Mode = "fallback copies" | "both paths fail" | "clipboard api present"

const SECRETS = {
  enrollment: "dpe_fixture_token",
  token: "dpt_acceptance_secret_0123456789",
  tailnet: "tskey-auth-acceptance-0123456789",
}

const url = (path: string) => `http://127.0.0.1:${process.env.E2E_PORT}${path}`

declare global {
  interface Window {
    __copied?: string
    __apiCopied?: string
    __execCalls?: number
  }
}

async function setUp(page: Page, mode: Mode) {
  await page.addInitScript((m) => {
    window.__execCalls = 0
    if (m === "clipboard api present") {
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: { writeText: (text: string) => { window.__apiCopied = text; return Promise.resolve() } },
      })
    } else {
      Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined })
    }
    document.execCommand = (command: string) => {
      if (command !== "copy") return false
      window.__execCalls = (window.__execCalls ?? 0) + 1
      const el = document.activeElement
      if (el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement) {
        window.__copied = el.value.slice(el.selectionStart ?? 0, el.selectionEnd ?? 0)
      } else {
        window.__copied = window.getSelection()?.toString() ?? ""
      }
      return m !== "both paths fail"
    }
  }, mode)
  await mockApi(page, { signedIn: true, worstCase: true })
}

const NOW = "2026-11-19T10:00:00.000Z"
const LATER = "2026-12-19T10:00:00.000Z"

function plan(action: string) {
  return { planId: `plan-${action}`, hostId: null, action, version: 1, inputHash: "h", diff: [{ op: "+", text: action }], guards: [], expiresAt: LATER, confirmation: null }
}

function change(action: string, output: unknown) {
  return {
    id: `change-${action}`, hostId: null, action, version: 1, class: "irreversible", state: "completed", actor: "admin",
    createdAt: NOW, updatedAt: NOW, revertDeadline: null, output, error: null, compensatedBy: null, parentChangeId: null,
    owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
}

// answerActions answers the named actions of the fixture API on top of
// mockApi: reads with output, plans with a plan and applies with a change.
async function answerActions(page: Page, answers: Record<string, { read?: unknown; apply?: unknown }>) {
  await page.route(
    (u) => u.pathname.startsWith("/api/v1/actions/"),
    async (route) => {
      const m = /^\/api\/v1\/actions\/([^/]+)\/v1\/(read|plan|apply)$/.exec(new URL(route.request().url()).pathname)
      const found = m === null ? undefined : answers[m[1] ?? ""]
      if (m === null || found === undefined) {
        await route.fallback()
        return
      }
      const op = m[2]
      const json = op === "read" ? { output: found.read } : op === "plan" ? plan(m[1] ?? "") : change(m[1] ?? "", found.apply)
      await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(json) })
    },
  )
}

async function openEnrollment(page: Page): Promise<Locator> {
  await page.goto(url(`/hosts/${PENDING_HOST_ID}`))
  await page.getByRole("button", { name: "New enrollment token" }).click()
  await page.getByLabel("Controller address (optional)").fill("panel.example.com:7443")
  await page.keyboard.press("Enter")
  const confirm = page.getByRole("dialog")
  await confirm.getByLabel("Type new-1 to confirm").fill("new-1")
  await confirm.getByRole("button", { name: "Apply" }).click()
  return page.getByLabel("Enrollment token for new-1")
}

async function openApiToken(page: Page): Promise<Locator> {
  await answerActions(page, {
    "core.token.list": { read: { tokens: [] } },
    "core.token.create": {
      apply: { id: "01JTOKEN", name: "ci", scope: "read", createdAt: NOW, expiresAt: LATER, lastUsedAt: null, revokedAt: null, token: SECRETS.token },
    },
  })
  await page.goto(url("/settings/tokens"))
  await page.getByRole("button", { name: "Create token" }).first().click()
  await page.getByLabel("Name").fill("ci")
  await page.getByRole("dialog").getByRole("button", { name: "Create" }).click()
  return page.getByLabel("Token ci")
}

async function openTailnetKey(page: Page): Promise<Locator> {
  await answerActions(page, {
    "tailnet.account.get": {
      read: { connected: true, account: { tailnet: "example.ts.net", clientId: "client-1", tagsAllowed: ["tag:server"], capabilities: [], connectedAt: NOW } },
    },
    "tailnet.authkey.list": { read: { keys: [] } },
    "tailnet.authkey.create": { apply: { id: "k1", key: SECRETS.tailnet, expires: LATER, description: "acceptance" } },
  })
  await page.goto(url("/tailnet/keys"))
  await page.getByLabel("tag:server").check()
  await page.getByRole("button", { name: "Create key" }).click()
  await page.getByRole("dialog").getByRole("button", { name: "Apply" }).click()
  return page.getByLabel("Auth key acceptance")
}

const DIALOGS: Array<{ name: string; secret: string; open: (page: Page) => Promise<Locator> }> = [
  { name: "enrollment-token", secret: SECRETS.enrollment, open: openEnrollment },
  { name: "API-token", secret: SECRETS.token, open: openApiToken },
  { name: "tailnet-key", secret: SECRETS.tailnet, open: openTailnetKey },
]

for (const dialog of DIALOGS) {
  test(`A13: ${dialog.name} dialog, Copy without navigator.clipboard copies through execCommand and shows the copied state`, async ({ page }) => {
    await setUp(page, "fallback copies")
    const field = await dialog.open(page)
    await expect(field).toHaveValue(dialog.secret)
    const copy = page.getByRole("dialog").getByRole("button", { name: /^Copy/ })
    await copy.click()
    await expect(copy).toHaveAttribute("data-copied", "true")
    await expect(page.getByRole("dialog").getByRole("status").filter({ hasText: "Copied" })).toHaveCount(1)
    expect(await page.evaluate(() => window.__copied)).toBe(dialog.secret)
  })

  test(`A13: ${dialog.name} dialog, Copy with both paths failing shows the error feedback and keeps the secret selectable`, async ({ page }) => {
    await setUp(page, "both paths fail")
    const field = await dialog.open(page)
    await expect(field).toHaveValue(dialog.secret)
    const copy = page.getByRole("dialog").getByRole("button", { name: /^Copy/ })
    await copy.click()
    await expect(copy).toHaveAttribute("data-copied", "false")
    expect(await page.evaluate(() => window.__execCalls)).toBeGreaterThan(0)
    await expect(page.getByText(/failed to copy|could not copy|copy failed|unable to copy/i).first()).toBeVisible({ timeout: 3_000 })
    await expect(field).toBeVisible()
    await field.focus()
    await field.evaluate((el: HTMLInputElement) => el.select())
    expect(await field.evaluate((el: HTMLInputElement) => el.value.slice(el.selectionStart ?? 0, el.selectionEnd ?? 0))).toBe(dialog.secret)
  })

  test(`A13: ${dialog.name} dialog, Copy with navigator.clipboard present uses it`, async ({ page }) => {
    await setUp(page, "clipboard api present")
    const field = await dialog.open(page)
    await expect(field).toHaveValue(dialog.secret)
    const copy = page.getByRole("dialog").getByRole("button", { name: /^Copy/ })
    await copy.click()
    await expect(copy).toHaveAttribute("data-copied", "true")
    expect(await page.evaluate(() => window.__apiCopied)).toBe(dialog.secret)
    expect(await page.evaluate(() => window.__execCalls)).toBe(0)
  })
}
