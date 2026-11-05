import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { AuditEntry } from "@/api/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { AuditPage } from "./audit"

const AUDIT = "POST /api/v1/actions/core.audit.list/v1/read"

function entry(id: string, action: string, extra: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id, at: "2026-10-19T08:00:00Z", actor: "user:u1", hostId: null, action, version: 1, phase: "apply",
    changeId: null, input: {}, result: "ok", errorCode: null, source: "127.0.0.1", ...extra,
  }
}

function pages(call: FetchCall): StubResponse {
  const input = (call.body as { input: { before?: string } }).input
  return input.before === undefined
    ? { body: { output: { entries: [entry("a2", "core.token.create", { hostId: "h1", changeId: "c2" })], nextBefore: "a2" } } }
    : { body: { output: { entries: [entry("a1", "core.auth.login", { result: "error", errorCode: "auth.invalid_credentials" })], nextBefore: null } } }
}

const failure: StubResponse = { status: 500, body: { type: "about:blank", title: "Internal", status: 500, detail: "older page failed", code: "internal" } }

test("the audit log passes axe with entries, a linked change and a failed result", async () => {
  stubFetch({ [AUDIT]: pages })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByText("core.token.create/v1")
  await expectNoAxeViolations(container)
  await userEvent.setup().click(screen.getByRole("button", { name: "Load older entries" }))
  await screen.findByText("core.auth.login/v1")
  await expectNoAxeViolations(container)
})

test("the audit log passes axe while it loads", async () => {
  stubFetch({ [AUDIT]: () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByLabelText("Loading the audit log")
  await expectNoAxeViolations(container)
})

test("the empty audit log passes axe", async () => {
  stubFetch({ [AUDIT]: { body: { output: { entries: [], nextBefore: null } } } })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByText("No audit entries yet")
  await expectNoAxeViolations(container)
})

test("the audit log passes axe when the first page fails", async () => {
  stubFetch({ [AUDIT]: failure })
  const { container } = renderWithProviders(<AuditPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("older page failed")
  await expectNoAxeViolations(container)
})

test("the audit log passes axe when an older page fails beside the loaded entries", async () => {
  stubFetch({ [AUDIT]: (call) => ((call.body as { input: { before?: string } }).input.before === undefined ? pages(call) : failure) })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByText("core.token.create/v1")
  await userEvent.setup().click(screen.getByRole("button", { name: "Load older entries" }))
  expect((await screen.findByRole("alert")).textContent).toBe("older page failed")
  await expectNoAxeViolations(container)
})

test("the keyboard reaches the change link and then the load button", async () => {
  stubFetch({ [AUDIT]: pages })
  renderWithProviders(<AuditPage />)
  const user = userEvent.setup()
  await screen.findByText("core.token.create/v1")
  expect(await tabSequence(user, 2)).toEqual(["core.token.create/v1", "Load older entries"])
})

test("the audit log has a level-one heading, named columns and the page title Audit log", async () => {
  stubFetch({ [AUDIT]: pages })
  renderWithProviders(<AuditPage />)
  await screen.findByText("core.token.create/v1")
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["Audit log"])
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Time", "Actor", "Host", "Action", "Result"])
  expect(document.title).toBe("Audit log · Darkpanel")
})
