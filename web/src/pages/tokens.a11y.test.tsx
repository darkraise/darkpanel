import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Plan, TokenInfo } from "@/api/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { TokensPage } from "./tokens"

const LIST = "POST /api/v1/actions/core.token.list/v1/read"
const REVOKE_PLAN = "POST /api/v1/actions/core.token.revoke/v1/plan"

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

const revokePlan: Plan = {
  planId: "p2", hostId: null, action: "core.token.revoke", version: 1, inputHash: "h",
  diff: [{ op: "-", text: "token ci" }], guards: [], expiresAt: "2026-10-19T09:00:00Z", confirmation: "tok1",
}

test("the token list passes axe with active, revoked and expired tokens", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } } })
  const { container } = renderWithProviders(<TokensPage />)
  await screen.findByText("ci")
  await expectNoAxeViolations(container)
})

test("the token list passes axe while it loads", async () => {
  stubFetch({ [LIST]: () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(<TokensPage />)
  await screen.findByLabelText("Loading the tokens")
  await expectNoAxeViolations(container)
})

test("the empty token list passes axe", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens: [] } } } })
  const { container } = renderWithProviders(<TokensPage />)
  await screen.findByText("No API tokens yet")
  await expectNoAxeViolations(container)
})

test("the token list error passes axe", async () => {
  stubFetch({ [LIST]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } } })
  const { container } = renderWithProviders(<TokensPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  await expectNoAxeViolations(container)
})

test("the revoke dialog passes axe and gives the focus back to its button", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } }, [REVOKE_PLAN]: { body: revokePlan } })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  await user.click(screen.getByRole("button", { name: "Revoke token ci" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Type tok1 to confirm")
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Revoke token ci")
})

test("the keyboard reaches Create token, then each Revoke button, skipping revoked tokens", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } } })
  renderWithProviders(<TokensPage />)
  const user = userEvent.setup()
  await screen.findByText("ci")
  expect(await tabSequence(user, 3)).toEqual(["Create token", "Revoke token ci", "Revoke token stale"])
})

test("the token list has a level-one heading and the page title API tokens", async () => {
  stubFetch({ [LIST]: { body: { output: { tokens } } } })
  renderWithProviders(<TokensPage />)
  await screen.findByText("ci")
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["API tokens"])
  expect(document.title).toBe("API tokens · Darkpanel")
})
