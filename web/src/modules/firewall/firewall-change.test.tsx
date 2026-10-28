import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler, type StubResponse } from "@/test/utils"
import { FirewallChange } from "./firewall-change"

const FC_PLAN = "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan"
const FC_APPLY = "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/apply"
const FC_DETAIL = 'The rule "allow in tcp from any to any port 22" was added by hand; pass manual to delete it.'

function fcPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p-manual", hostId: "h1", action: "firewall.rule.delete", version: 1, inputHash: "h",
    diff: [{ op: "-", text: "allow in tcp from any to any port 22 (manual)" }], guards: [], expiresAt: "2026-10-28T09:00:00Z", confirmation: null, ...over,
  }
}

function fcRefusal(code = "firewall.manual_required", detail = FC_DETAIL): StubResponse {
  return { status: 409, body: { type: "about:blank", title: "Manual action required", status: 409, detail, code } }
}

// A plan handler that refuses unless the input carries manual: true.
function fcPlansWhenManual(call: FetchCall): StubResponse {
  const input = (call.body as { input: { manual?: boolean } }).input
  return input.manual === true ? { body: fcPlan() } : fcRefusal()
}

function FcHost({ input, onClosed }: { input: Record<string, unknown>; onClosed: () => void }) {
  const [open, setOpen] = useState<Record<string, unknown> | null>(null)
  return (
    <>
      <button onClick={() => setOpen(input)}>Open</button>
      <FirewallChange
        hostId="h1"
        action="firewall.rule.delete"
        title="Delete rule 2"
        input={open}
        onClose={() => {
          setOpen(null)
          onClosed()
        }}
      />
    </>
  )
}

function fcRender(routes: Record<string, StubHandler>, input: Record<string, unknown> = { id: "manual-0123456789abcdef" }) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.delete", available: true, reason: null }] },
    },
    ...routes,
  })
  const closed = { count: 0 }
  const result = renderWithProviders(<FcHost input={input} onClosed={() => (closed.count += 1)} />)
  return { calls, closed, ...result }
}

function fcPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

test("a plan refused for a manual rule shows the detail and asks before planning again", async () => {
  const { calls } = fcRender({ [FC_PLAN]: fcPlansWhenManual })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const ask = await screen.findByRole("dialog", { name: "Change a manual rule?" })
  expect(within(ask).getByText(FC_DETAIL).textContent).toBe(FC_DETAIL)
  expect(screen.queryByLabelText("Planned changes")).toBeNull()
  expect(fcPlanBodies(calls)).toEqual([{ input: { id: "manual-0123456789abcdef" } }])
})

test("confirming plans again with manual: true and applies that plan", async () => {
  const { calls } = fcRender({
    [FC_PLAN]: fcPlansWhenManual,
    [FC_APPLY]: { body: { id: "c1", state: "completed", class: "revertible", error: null, output: null, revertDeadline: null } },
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  const ask = await screen.findByRole("dialog", { name: "Change a manual rule?" })
  await user.click(within(ask).getByRole("button", { name: "Change it anyway" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule 2" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("- allow in tcp from any to any port 22 (manual)")
  expect(fcPlanBodies(calls)).toEqual([{ input: { id: "manual-0123456789abcdef" } }, { input: { id: "manual-0123456789abcdef", manual: true } }])
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p-manual", confirmTimeoutSeconds: 60 })
})

test("cancelling the question closes the change without planning again", async () => {
  const { calls, closed } = fcRender({ [FC_PLAN]: fcPlansWhenManual })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  const ask = await screen.findByRole("dialog", { name: "Change a manual rule?" })
  await user.click(within(ask).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(closed.count).toBe(1)
  expect(fcPlanBodies(calls)).toHaveLength(1)
})

test("a consent given once is not kept when the change is opened again", async () => {
  const { calls } = fcRender({ [FC_PLAN]: fcPlansWhenManual })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  await user.click(within(await screen.findByRole("dialog", { name: "Change a manual rule?" })).getByRole("button", { name: "Change it anyway" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule 2" })
  await within(dialog).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Open" }))
  await screen.findByRole("dialog", { name: "Change a manual rule?" })
  expect(fcPlanBodies(calls)).toEqual([
    { input: { id: "manual-0123456789abcdef" } },
    { input: { id: "manual-0123456789abcdef", manual: true } },
    { input: { id: "manual-0123456789abcdef" } },
  ])
})

test("a plan that needs no manual rule opens the change dialog at once", async () => {
  const { calls } = fcRender({ [FC_PLAN]: { body: fcPlan({ planId: "p1", diff: [{ op: "-", text: "allow in tcp from any to any port 443" }] }) } })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule 2" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("- allow in tcp from any to any port 443")
  expect(screen.queryByRole("dialog", { name: "Change a manual rule?" })).toBeNull()
  expect(fcPlanBodies(calls)).toHaveLength(1)
})

test("another refusal stays in the change dialog and asks nothing", async () => {
  fcRender({ [FC_PLAN]: fcRefusal("firewall.prediction_mismatch", "the predicted rules differ from UFW's") })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule 2" })
  expect((await within(dialog).findByRole("alert")).textContent).toBe("the predicted rules differ from UFW's")
  expect(screen.queryByRole("dialog", { name: "Change a manual rule?" })).toBeNull()
})

test("an input that already says manual is never asked about", async () => {
  const { calls } = fcRender({ [FC_PLAN]: fcRefusal() }, { id: "manual-0123456789abcdef", manual: true })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule 2" })
  expect((await within(dialog).findByRole("alert")).textContent).toBe(FC_DETAIL)
  expect(screen.queryByRole("dialog", { name: "Change a manual rule?" })).toBeNull()
  expect(fcPlanBodies(calls)).toEqual([{ input: { id: "manual-0123456789abcdef", manual: true } }])
})

test("the question has no accessibility violations", async () => {
  fcRender({ [FC_PLAN]: fcPlansWhenManual })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const ask = await screen.findByRole("dialog", { name: "Change a manual rule?" })
  expect(await axe(ask)).toHaveNoViolations()
})
