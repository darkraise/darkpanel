import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { changeQueryKey } from "@/api/client"
import type { Change, Plan, Step } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { undoOffered } from "@/lib/undo"
import { ChangeActions } from "./change-actions"

function cacChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cacRender(change: Change, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch(routes)
  const result = renderWithProviders(<ChangeActions change={change} />)
  return { calls, ...result }
}

const problem = (status: number, code: string, detail: string) => ({ status, body: { type: "about:blank", title: code, status, detail, code } })

test.each([
  ["a completed compensable change", {}, true],
  ["a failed one", { state: "failed" }, true],
  ["an interrupted one", { state: "interrupted" }, true],
  ["one that needs repair", { state: "needs_repair" }, true],
  ["a revertible change", { class: "revertible" }, false],
  ["a composite", { class: "composite" }, false],
  ["an owned change", { owner: "engine" }, false],
  ["a composite's completed step", { parentChangeId: "p1" }, false],
  ["a composite's step that needs repair", { parentChangeId: "p1", state: "needs_repair" }, true],
  ["a reverted change", { state: "reverted" }, false],
  ["an applying change", { state: "applying" }, false],
] as Array<[string, Partial<Change>, boolean]>)("undo is offered for %s: %s", (_name, over, offered) => {
  expect(undoOffered(cacChange(over))).toBe(offered)
})

test("undo asks first and stores the answered change", async () => {
  const undoing = cacChange({ state: "undoing", owner: "undo" })
  const { calls, client } = cacRender(cacChange(), { "POST /api/v1/changes/c1/undo": { status: 202, body: undoing } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Undo" }))
  const dialog = screen.getByRole("dialog", { name: "Undo this change?" })
  await user.click(within(dialog).getByRole("button", { name: "Undo" }))
  await waitFor(() => expect(client.getQueryData(changeQueryKey("c1"))).toEqual(undoing))
  expect(calls.filter((c) => c.path === "/api/v1/changes/c1/undo").length).toBe(1)
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("a refused undo shows the reason", async () => {
  cacRender(cacChange(), { "POST /api/v1/changes/c1/undo": problem(409, "core.undo_conflict", "lock firewall was taken by a later change") })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Undo" }))
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Undo" }))
  expect((await screen.findByRole("alert")).textContent).toBe("lock firewall was taken by a later change")
})

test("a change that needs no repair and cannot be undone offers nothing", () => {
  cacRender(cacChange({ class: "revertible", state: "confirmed" }))
  expect(screen.queryByRole("region", { name: "Actions" })).toBeNull()
})

test("repair applies the plan it answers through the change dialog", async () => {
  const plan: Plan = {
    planId: "rp1", hostId: "h1", action: "cdx.rule.fix", version: 1, inputHash: "h", diff: [{ op: "+", text: "restore rule 443" }],
    guards: [], expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
  }
  const { calls } = cacRender(cacChange({ state: "needs_repair" }), {
    "POST /api/v1/changes/c1/repair": { body: { plan } },
    "POST /api/v1/hosts/h1/actions/cdx.rule.fix/v1/apply": { body: cacChange({ id: "r1", action: "cdx.rule.fix" }) },
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Repair" }))
  const dialog = await screen.findByRole("dialog", { name: "Repair the change" })
  expect(within(dialog).getByLabelText("Planned changes").textContent).toBe("+ restore rule 443")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "rp1" }))
  expect(calls.find((c) => c.path.endsWith("/repair"))?.body).toEqual({})
  expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
})

test("a repair that starts in place stores the change it answers", async () => {
  const recovering = cacChange({ state: "recovering", owner: "repair" })
  const { client } = cacRender(cacChange({ state: "needs_repair" }), { "POST /api/v1/changes/c1/repair": { status: 202, body: { change: recovering } } })
  await userEvent.setup().click(screen.getByRole("button", { name: "Repair" }))
  await waitFor(() => expect(client.getQueryData(changeQueryKey("c1"))).toEqual(recovering))
})

test("release needs a reason and reports the abandoned effects", async () => {
  const released = cacChange({ state: "abandoned" })
  const { calls, client } = cacRender(cacChange({ state: "needs_repair" }), {
    "POST /api/v1/changes/c1/release": { body: { change: released, abandoned: [{ seq: 1 }] } },
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Release" }))
  const dialog = screen.getByRole("dialog", { name: "Release this change?" })
  await user.type(within(dialog).getByLabelText("Reason"), "  the key was deleted by hand ")
  await user.click(within(dialog).getByRole("button", { name: "Release" }))
  expect((await screen.findByText("Released. 1 effect abandoned.")).getAttribute("role")).toBe("status")
  expect(calls.find((c) => c.path.endsWith("/release"))?.body).toEqual({ reason: "the key was deleted by hand" })
  expect(client.getQueryData(changeQueryKey("c1"))).toEqual(released)
})

test("a refused release stays open with the reason", async () => {
  cacRender(cacChange({ state: "needs_repair" }), { "POST /api/v1/changes/c1/release": problem(403, "auth.forbidden", "release needs the admin scope") })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Release" }))
  const dialog = screen.getByRole("dialog", { name: "Release this change?" })
  await user.type(within(dialog).getByLabelText("Reason"), "stuck")
  await user.click(within(dialog).getByRole("button", { name: "Release" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("release needs the admin scope")
})

test("a composite that needs repair decides one step", async () => {
  const steps: Step[] = [
    { id: "proxy", ordinal: 2, host: "h1", action: "cdx.proxy.add/v1", state: "applying", attempt: 1, childChangeId: "k2", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "network", ordinal: 1, host: "h1", action: "cdx.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const { calls } = cacRender(cacChange({ class: "composite", state: "needs_repair" }), {
    "GET /api/v1/changes/c1/steps": { body: { steps } },
    "GET /api/v1/changes/c1": { body: cacChange({ class: "composite", state: "applying" }) },
    "POST /api/v1/changes/c1/repair": { status: 202, body: { change: cacChange({ class: "composite", state: "applying" }) } },
  })
  const user = userEvent.setup()
  expect(screen.queryByRole("button", { name: "Undo" })).toBeNull()
  const decide = await screen.findByRole("button", { name: "Decide step" })
  expect(decide.hasAttribute("disabled")).toBe(true)
  await user.click(screen.getByRole("radio", { name: "proxy (applying)" }))
  await user.click(screen.getByRole("radio", { name: "Applied" }))
  await user.click(decide)
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/repair"))?.body).toEqual({ step: "proxy", as: "applied" }))
})
