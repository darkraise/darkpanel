import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { Change, Guard, Plan, Step } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangeDialog, type ChangeDialogProps } from "./change-dialog"

const PLAN = "POST /api/v1/hosts/h1/actions/cdx.rule.set/v1/plan"
const APPLY = "POST /api/v1/hosts/h1/actions/cdx.rule.set/v1/apply"
const AVAILABILITY = "GET /api/v1/hosts/h1/modules/cdx/availability"

const cdxAvailable = { module: "cdx", available: true, reason: null, actions: [{ name: "cdx.rule.set", available: true, reason: null }] }

function cdxPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: "h1", action: "cdx.rule.set", version: 1, inputHash: "h",
    diff: [{ op: "+", text: "allow 443/tcp" }, { op: " ", text: "default deny" }], guards: [],
    expiresAt: "2026-10-23T09:00:00Z", confirmation: null, ...over,
  }
}

function cdxGuard(over: Partial<Guard> = {}): Guard {
  return { id: "ssh-session", ok: false, message: "this change blocks your SSH session", overridable: true, overridden: false, ...over }
}

function cdxChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "revertible", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cdxRender(routes: Record<string, StubHandler>, props: Partial<ChangeDialogProps> = {}) {
  const calls = stubFetch({ [AVAILABILITY]: { body: cdxAvailable }, ...routes })
  const onOpenChange = vi.fn()
  const onApplied = vi.fn()
  const result = renderWithProviders(
    <ChangeDialog
      open
      onOpenChange={onOpenChange}
      title="Allow HTTPS"
      target={{ name: "cdx.rule.set", version: 1, hostId: "h1", module: "cdx" }}
      input={{ port: 443 }}
      onApplied={onApplied}
      {...props}
    />,
  )
  return { calls, onOpenChange, onApplied, ...result }
}

afterEach(() => {
  vi.useRealTimers()
})

test("plans on open, shows the diff and applies the plan", async () => {
  const { calls, onApplied } = cdxRender({ [PLAN]: { body: cdxPlan() }, [APPLY]: { body: cdxChange() } })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ allow 443/tcp  default deny")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { port: 443 } })
  await userEvent.setup().click(within(dialog).getByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByText("completed")).textContent).toBe("completed")
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
  expect(onApplied).toHaveBeenCalledWith(cdxChange())
  expect(within(dialog).getByRole("link", { name: "View change" }).getAttribute("href")).toBe("/changes/c1")
})

test("a failed overridable guard can be overridden, which plans again with overrides", async () => {
  const plans: unknown[] = []
  cdxRender({
    [PLAN]: (call) => {
      plans.push(call.body)
      const overridden = plans.length > 1
      return { body: cdxPlan({ planId: `p${plans.length}`, guards: [cdxGuard({ ok: overridden, overridden })] }) }
    },
    [APPLY]: { body: cdxChange() },
  })
  const dialog = await screen.findByRole("dialog")
  const user = userEvent.setup()
  expect((await within(dialog).findByText("failed")).textContent).toBe("failed")
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
  await user.click(within(dialog).getByRole("checkbox", { name: "Override ssh-session" }))
  expect((await within(dialog).findByText("overridden")).textContent).toBe("overridden")
  expect(plans).toEqual([{ input: { port: 443 } }, { input: { port: 443, overrides: ["ssh-session"] } }])
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
})

test("a failed guard that is not overridable blocks the apply and offers no override", async () => {
  cdxRender({ [PLAN]: { body: cdxPlan({ guards: [cdxGuard({ overridable: false })] }) } })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByText("this change blocks your SSH session")).textContent).toBe("this change blocks your SSH session")
  expect(within(dialog).queryByRole("checkbox")).toBeNull()
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("an irreversible plan applies only after its phrase is typed", async () => {
  const { calls } = cdxRender({ [PLAN]: { body: cdxPlan({ confirmation: "web-1" }) }, [APPLY]: { body: cdxChange() } })
  const dialog = await screen.findByRole("dialog")
  const user = userEvent.setup()
  const phrase = await within(dialog).findByLabelText("Type web-1 to confirm")
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await user.type(phrase, "web-")
  expect(apply.hasAttribute("disabled")).toBe(true)
  await user.type(phrase, "1")
  expect(apply.hasAttribute("disabled")).toBe(false)
  await user.click(apply)
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1", confirmation: "web-1" }))
})

test("a pending change counts down to its revert and can be confirmed", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date("2026-10-23T08:00:00Z"))
  const pending = cdxChange({ state: "pending", revertDeadline: "2026-10-23T08:01:00Z" })
  const { calls } = cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { body: pending },
    "GET /api/v1/changes/c1": () => ({ body: calls.some((c) => c.path.endsWith("/confirm")) ? cdxChange({ state: "confirmed" }) : pending }),
    "POST /api/v1/changes/c1/confirm": { body: cdxChange({ state: "confirmed" }) },
  })
  const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByRole("timer")).textContent).toBe("Reverts automatically in 60 s unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000)
  })
  expect(within(dialog).getByRole("timer").textContent).toBe("Reverts automatically in 55 s unless confirmed.")
  await user.click(within(dialog).getByRole("button", { name: "Confirm" }))
  expect((await within(dialog).findByText("confirmed")).textContent).toBe("confirmed")
  expect(within(dialog).queryByRole("timer")).toBeNull()
})

test("a pending change can be reverted", async () => {
  const pending = cdxChange({ state: "pending", revertDeadline: "2099-01-01T00:00:00Z" })
  const { calls } = cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { body: pending },
    "GET /api/v1/changes/c1": () => ({ body: calls.some((c) => c.path.endsWith("/revert")) ? cdxChange({ state: "reverted" }) : pending }),
    "POST /api/v1/changes/c1/revert": { body: cdxChange({ state: "reverted" }) },
  })
  const user = userEvent.setup()
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await user.click(await within(dialog).findByRole("button", { name: "Revert" }))
  expect((await within(dialog).findByText("reverted")).textContent).toBe("reverted")
})

test("an asynchronous composite apply answers 202 and shows its steps", async () => {
  const steps: Step[] = [
    { id: "ports", ordinal: 2, host: "h1", action: "cdx.ports.open/v1", state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "network", ordinal: 1, host: "h1", action: "cdx.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const applying = cdxChange({ class: "composite", state: "applying" })
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { status: 202, body: applying },
    "GET /api/v1/changes/c1": { body: applying },
    "GET /api/v1/changes/c1/steps": { body: { steps } },
  })
  const user = userEvent.setup()
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByText("applying")).textContent).toBe("applying")
  const list = await within(dialog).findByRole("list", { name: "Steps" })
  expect(within(list).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "network (cdx.net.create/v1)applied",
    "ports (cdx.ports.open/v1)pending",
  ])
})

test("an action unavailable on the host is greyed out with the reason", async () => {
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [AVAILABILITY]: { body: { ...cdxAvailable, actions: [{ name: "cdx.rule.set", available: false, reason: "ufw is not installed" }] } },
  })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByText("ufw is not installed")).textContent).toBe("ufw is not installed")
  await within(dialog).findByLabelText("Planned changes")
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("a refused plan and a failed apply are shown", async () => {
  const problem = { type: "about:blank", title: "Locked", status: 423, detail: "lock firewall is held by change c0", code: "lock.held" }
  cdxRender({ [PLAN]: { status: 423, body: problem } })
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByRole("alert")).textContent).toBe("lock firewall is held by change c0")
})

test("a change that failed shows its error", async () => {
  cdxRender({
    [PLAN]: { body: cdxPlan() },
    [APPLY]: { body: cdxChange({ state: "failed", error: { code: "internal", message: "ufw exited with status 1" } }) },
  })
  const dialog = await screen.findByRole("dialog")
  await userEvent.setup().click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("ufw exited with status 1")
})

test("a plan given as a prop is shown and applied without planning", async () => {
  const { calls } = cdxRender({ [APPLY]: { body: cdxChange() } }, { input: undefined, plan: cdxPlan({ planId: "r1" }) })
  const dialog = await screen.findByRole("dialog")
  await userEvent.setup().click(await within(dialog).findByRole("button", { name: "Apply" }))
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "r1" }))
  expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
})

test("the planned dialog has no accessibility violations", async () => {
  cdxRender({ [PLAN]: { body: cdxPlan({ guards: [cdxGuard()], confirmation: "web-1" }) } })
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(await axe(dialog)).toHaveNoViolations()
})
