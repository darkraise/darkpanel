import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Change, Plan, TokenInfo } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { TokensPage } from "./tokens"

const LIST = "POST /api/v1/actions/core.token.list/v1/read"
const PLAN = "POST /api/v1/actions/core.token.revoke/v1/plan"
const APPLY = "POST /api/v1/actions/core.token.revoke/v1/apply"

function token(id: string, name: string, extra: Partial<TokenInfo> = {}): TokenInfo {
  return {
    id, name, scope: "read", createdAt: "2026-10-01T08:00:00Z", expiresAt: "2999-01-01T00:00:00Z",
    lastUsedAt: null, revokedAt: null, ...extra,
  }
}

const tokens = [
  token("tok1", "ci"),
  token("tok2", "old", { revokedAt: "2026-10-10T08:00:00Z" }),
  token("tok3", "stale", { expiresAt: "2000-01-01T00:00:00Z" }),
]

const plan: Plan = {
  planId: "p2", hostId: null, action: "core.token.revoke", version: 1, inputHash: "h",
  diff: [{ op: "-", text: "token ci" }], guards: [], expiresAt: "2026-10-19T09:00:00Z", confirmation: "tok1",
}

const revoked: Change = {
  id: "c2", hostId: null, action: "core.token.revoke", version: 1, class: "irreversible", state: "completed", actor: "user:u1",
  createdAt: "2026-10-19T08:00:00Z", updatedAt: "2026-10-19T08:00:00Z", revertDeadline: null,
  output: { ...tokens[0], revokedAt: "2026-10-19T08:00:00Z" }, error: null, compensatedBy: null,
  parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function rowOf(name: string): HTMLElement {
  const row = screen.getByText(name).closest("tr")
  if (row === null) throw new Error(`no row for ${name}`)
  return row
}

test("lists tokens with their state and the create button", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } } })
  renderWithProviders(<TokensPage />)
  await screen.findByText("ci")
  expect(within(rowOf("ci")).getByText("Active").textContent).toBe("Active")
  expect(within(rowOf("old")).getByText("Revoked").textContent).toBe("Revoked")
  expect(within(rowOf("stale")).getByText("Expired").textContent).toBe("Expired")
  expect(within(rowOf("old")).queryByRole("button", { name: "Revoke token old" })).toBeNull()
  expect(within(rowOf("ci")).getByRole("button", { name: "Revoke token ci" }).textContent).toBe("Revoke")
  expect(within(rowOf("stale")).getByRole("button", { name: "Revoke token stale" }).textContent).toBe("Revoke")
  expect(screen.getByRole("button", { name: "Create token" })).toBeTruthy()
  expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("API tokens")
})

test("revoking needs the typed confirmation phrase", async () => {
  let listCalls = 0
  const calls = stubFetch({
    [LIST]: () => {
      listCalls++
      return { body: { output: { tokens } } }
    },
    [PLAN]: { body: plan },
    [APPLY]: { body: revoked },
  })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  await user.click(within(rowOf("ci")).getByRole("button", { name: "Revoke token ci" }))

  const dialog = screen.getByRole("dialog")
  const phrase = await within(dialog).findByLabelText("Type tok1 to confirm")
  const confirm = within(dialog).getByRole("button", { name: "Revoke" })
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { id: "tok1" } })
  expect(confirm.hasAttribute("disabled")).toBe(true)

  await user.type(phrase, "tok")
  expect(confirm.hasAttribute("disabled")).toBe(true)
  await user.type(phrase, "1")
  expect(confirm.hasAttribute("disabled")).toBe(false)
  await user.click(confirm)

  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p2", confirmation: "tok1" })
  await waitFor(() => expect(listCalls).toBe(2))
})

test("a failed revocation keeps the dialog open with the error", async () => {
  stubFetch({
    [LIST]: { body: { output: { tokens } } },
    [PLAN]: { body: plan },
    [APPLY]: { status: 410, body: { type: "about:blank", title: "Plan expired", status: 410, detail: "the plan expired; plan again", code: "plan.expired" } },
  })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  await user.click(within(rowOf("ci")).getByRole("button", { name: "Revoke token ci" }))
  const dialog = screen.getByRole("dialog")
  await user.type(await within(dialog).findByLabelText("Type tok1 to confirm"), "tok1")
  await user.click(within(dialog).getByRole("button", { name: "Revoke" }))
  expect((await within(dialog).findByText("the plan expired; plan again")).textContent).toBe("the plan expired; plan again")
})

test("an expired plan is replaced by a fresh one and the typed phrase is cleared", async () => {
  let plans = 0
  const calls = stubFetch({
    [LIST]: { body: { output: { tokens } } },
    [PLAN]: () => {
      plans++
      return { body: { ...plan, planId: `p${plans}` } }
    },
    [APPLY]: (call) =>
      (call.body as { planId: string }).planId === "p1"
        ? { status: 410, body: { type: "about:blank", title: "Plan expired", status: 410, detail: "the plan expired; plan again", code: "plan.expired" } }
        : { body: revoked },
  })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  await user.click(within(rowOf("ci")).getByRole("button", { name: "Revoke token ci" }))
  const dialog = screen.getByRole("dialog")
  const phrase = await within(dialog).findByLabelText("Type tok1 to confirm")
  await user.type(phrase, "tok1")
  await user.click(within(dialog).getByRole("button", { name: "Revoke" }))

  await within(dialog).findByText("the plan expired; plan again")
  await waitFor(() => expect(plans).toBe(2))
  await waitFor(() => expect((within(dialog).getByLabelText("Type tok1 to confirm") as HTMLInputElement).value).toBe(""))

  await user.type(within(dialog).getByLabelText("Type tok1 to confirm"), "tok1")
  await user.click(within(dialog).getByRole("button", { name: "Revoke" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(calls.filter((c) => c.path.endsWith("/apply")).map((c) => (c.body as { planId: string }).planId)).toEqual(["p1", "p2"])
})

test("Escape does not close the dialog while a revocation is in flight", async () => {
  let release: () => void = () => undefined
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  stubFetch({
    [LIST]: { body: { output: { tokens } } },
    [PLAN]: { body: plan },
    [APPLY]: async () => {
      await gate
      return { body: revoked }
    },
  })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  await user.click(within(rowOf("ci")).getByRole("button", { name: "Revoke token ci" }))
  const dialog = screen.getByRole("dialog")
  await user.type(await within(dialog).findByLabelText("Type tok1 to confirm"), "tok1")
  await user.click(within(dialog).getByRole("button", { name: "Revoke" }))

  await user.keyboard("{Escape}")
  await new Promise((resolve) => setTimeout(resolve, 50))
  expect(screen.getByRole("dialog")).toBeTruthy()

  release()
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
})
