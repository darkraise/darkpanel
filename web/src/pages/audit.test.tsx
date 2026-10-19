import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { AuditEntry } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { AuditPage } from "./audit"

const AUDIT = "POST /api/v1/actions/core.audit.list/v1/read"

function entry(id: string, action: string, extra: Partial<AuditEntry> = {}): AuditEntry {
  return {
    id, at: "2026-10-19T08:00:00Z", actor: "user:u1", hostId: null, action, version: 1, phase: "apply",
    changeId: null, input: {}, result: "ok", errorCode: null, source: "127.0.0.1", ...extra,
  }
}

function pages(call: FetchCall) {
  const input = (call.body as { input: { before?: string } }).input
  return input.before === undefined
    ? { body: { output: { entries: [entry("a2", "core.token.create", { hostId: "h1" })], nextBefore: "a2" } } }
    : { body: { output: { entries: [entry("a1", "core.auth.login", { result: "error", errorCode: "auth.invalid_credentials" })], nextBefore: null } } }
}

test("lists entries newest first and loads older ones by nextBefore", async () => {
  const calls = stubFetch({ [AUDIT]: pages })
  renderWithProviders(<AuditPage />)
  expect((await screen.findByText("core.token.create/v1")).textContent).toBe("core.token.create/v1")
  expect(screen.getByText("h1").textContent).toBe("h1")
  expect(calls[0]?.body).toEqual({ input: { limit: 50 } })

  await userEvent.setup().click(screen.getByRole("button", { name: "Load older entries" }))
  expect((await screen.findByText("core.auth.login/v1")).textContent).toBe("core.auth.login/v1")
  expect(screen.getByText("auth.invalid_credentials").textContent).toBe("auth.invalid_credentials")
  expect(calls[1]?.body).toEqual({ input: { limit: 50, before: "a2" } })
  expect(screen.queryByRole("button", { name: "Load older entries" })).toBeNull()
  expect(screen.getAllByRole("row")).toHaveLength(3)
})

test("keeps loaded entries when an older page fails", async () => {
  stubFetch({
    [AUDIT]: (call) =>
      (call.body as { input: { before?: string } }).input.before === undefined
        ? pages(call)
        : { status: 500, body: { type: "about:blank", title: "Internal", status: 500, detail: "older page failed", code: "internal" } },
  })
  renderWithProviders(<AuditPage />)
  await screen.findByText("core.token.create/v1")
  await userEvent.setup().click(screen.getByRole("button", { name: "Load older entries" }))
  expect((await screen.findByText("older page failed")).textContent).toBe("older page failed")
  expect(screen.getByText("core.token.create/v1").textContent).toBe("core.token.create/v1")
  expect(screen.getByRole("button", { name: "Load older entries" }).textContent).toBe("Load older entries")
})

test("says so when there are no entries", async () => {
  stubFetch({ [AUDIT]: { body: { output: { entries: [], nextBefore: null } } } })
  renderWithProviders(<AuditPage />)
  expect((await screen.findByText("No audit entries yet")).textContent).toBe("No audit entries yet")
})

test("shows the server's reason when the read fails", async () => {
  stubFetch({
    [AUDIT]: { status: 403, body: { type: "about:blank", title: "Forbidden", status: 403, detail: "this request needs the read scope", code: "auth.forbidden" } },
  })
  renderWithProviders(<AuditPage />)
  expect((await screen.findByText("this request needs the read scope")).textContent).toBe("this request needs the read scope")
})
