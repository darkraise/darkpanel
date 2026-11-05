import { useState } from "react"
import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import type { Change, Guard, Plan, Step } from "@/api/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangeDialog, type ChangeDialogProps } from "./change-dialog"

const PLAN = "POST /api/v1/hosts/h1/actions/cda.rule.set/v1/plan"
const APPLY = "POST /api/v1/hosts/h1/actions/cda.rule.set/v1/apply"
const AVAILABILITY = "GET /api/v1/hosts/h1/modules/cda/availability"

const cdaAvailable = { module: "cda", available: true, reason: null, actions: [{ name: "cda.rule.set", available: true, reason: null }] }
const cdaTarget = { name: "cda.rule.set", version: 1, hostId: "h1", module: "cda", revertible: true }

function cdaPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: "h1", action: "cda.rule.set", version: 1, inputHash: "h",
    diff: [{ op: "+", text: "allow 443/tcp" }, { op: " ", text: "default deny" }], guards: [],
    expiresAt: "2026-10-23T09:00:00Z", confirmation: null, ...over,
  }
}

function cdaGuard(over: Partial<Guard> = {}): Guard {
  return { id: "ssh-session", ok: false, message: "this change blocks your SSH session", overridable: true, overridden: false, ...over }
}

function cdaChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cda.rule.set", version: 1, class: "revertible", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

// CdaHarness opens the dialog from a button, as every caller does, so the
// focus the dialog must give back has an element to return to.
function CdaHarness(props: Partial<ChangeDialogProps>) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open the change
      </button>
      <ChangeDialog open={open} onOpenChange={setOpen} title="Allow HTTPS" target={cdaTarget} input={{ port: 443 }} {...props} />
    </>
  )
}

async function cdaOpen(routes: Record<string, StubHandler>, props: Partial<ChangeDialogProps> = {}, setup: Parameters<typeof userEvent.setup>[0] = {}) {
  const calls = stubFetch({ [AVAILABILITY]: { body: cdaAvailable }, ...routes })
  const user = userEvent.setup(setup)
  renderWithProviders(<CdaHarness {...props} />)
  await user.click(screen.getByRole("button", { name: "Open the change" }))
  const dialog = await screen.findByRole("dialog", { name: "Allow HTTPS" })
  return { calls, user, dialog }
}

test("the unavailable and availability-unknown alerts keep the heading order", async () => {
  const off = await cdaOpen({
    [AVAILABILITY]: { body: { ...cdaAvailable, available: false, reason: "UFW is not installed." } },
    [PLAN]: { body: cdaPlan() },
  })
  await within(off.dialog).findByText("UFW is not installed.")
  await expectNoAxeViolations(off.dialog)
})

afterEach(() => {
  vi.useRealTimers()
})

test("a planned change with a failed guard and a typed confirmation has no violations", async () => {
  const { dialog } = await cdaOpen({ [PLAN]: { body: cdaPlan({ guards: [cdaGuard()], confirmation: "web-1" }) } })
  await within(dialog).findByRole("checkbox", { name: "Override ssh-session" })
  await expectNoAxeViolations(dialog)
})

test("a guard that was overridden has no violations", async () => {
  let plans = 0
  const { dialog, user } = await cdaOpen({
    [PLAN]: () => {
      plans++
      return { body: cdaPlan({ guards: [cdaGuard({ ok: plans > 1, overridden: plans > 1 })] }) }
    },
  })
  await user.click(await within(dialog).findByRole("checkbox", { name: "Override ssh-session" }))
  await within(dialog).findByText("overridden")
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(false))
  await expectNoAxeViolations(dialog)
})

test("an applying composite with its steps has no violations", async () => {
  const steps: Step[] = [
    { id: "network", ordinal: 1, host: "h1", action: "cda.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "ports", ordinal: 2, host: "h1", action: "cda.ports.open/v1", state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  const applying = cdaChange({ class: "composite", state: "applying" })
  const { dialog, user } = await cdaOpen({
    [PLAN]: { body: cdaPlan() },
    [APPLY]: { status: 202, body: applying },
    "GET /api/v1/changes/c1": { body: applying },
    "GET /api/v1/changes/c1/steps": { body: { steps } },
  })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByRole("list", { name: "Steps" })
  await expectNoAxeViolations(dialog)
})

test("a pending change with its countdown, Confirm and Revert has no violations", async () => {
  const pending = cdaChange({ state: "pending", revertDeadline: "2099-01-01T00:00:00Z" })
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: pending }, "GET /api/v1/changes/c1": { body: pending } })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByRole("timer")
  expect(within(dialog).getByRole("button", { name: "Confirm" })).toBeTruthy()
  expect(within(dialog).getByRole("button", { name: "Revert" })).toBeTruthy()
  await expectNoAxeViolations(dialog)
})

test("a failed change has no violations", async () => {
  const failed = cdaChange({ state: "failed", error: { code: "internal", message: "ufw exited with status 1" } })
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: failed }, "GET /api/v1/changes/c1": { body: failed } })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("ufw exited with status 1")
  await expectNoAxeViolations(dialog)
})

test("Tab and Shift+Tab stay inside the dialog and wrap", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan({ guards: [cdaGuard()], confirmation: "web-1" }) } })
  await within(dialog).findByLabelText("Type web-1 to confirm")
  const forward: string[] = []
  for (let i = 0; i < 8; i++) {
    forward.push(...(await tabSequence(user, 1)))
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  expect(forward).toContain("Override ssh-session")
  expect(forward).toContain("Type web-1 to confirm")
  expect(new Set(forward).size).toBeLessThan(forward.length)
  const backward: string[] = []
  for (let i = 0; i < 8; i++) {
    backward.push(...(await tabSequence(user, 1, { backwards: true })))
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  expect(new Set(backward).size).toBeLessThan(backward.length)
})

test("Escape closes the dialog and returns focus to the button that opened it", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() } })
  await within(dialog).findByLabelText("Planned changes")
  expect(dialog.contains(document.activeElement)).toBe(true)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})

test("the dialog keeps focus after Apply and Close returns it to the opener", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: cdaChange() }, "GET /api/v1/changes/c1": { body: cdaChange() } })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  apply.focus()
  await user.keyboard("{Enter}")
  await within(dialog).findByText("completed")
  expect(dialog.contains(document.activeElement)).toBe(true)
  // The footer's Close comes before the corner button the dialog adds.
  const close = within(dialog).getAllByRole("button", { name: "Close" })[0]
  if (close === undefined) throw new Error("no Close button")
  await user.click(close)
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Open the change")
})

test("the typed-confirmation and revert-timeout fields have labels and the timeout its help", async () => {
  const { dialog } = await cdaOpen({ [PLAN]: { body: cdaPlan({ confirmation: "web-1" }) } })
  const phrase = await within(dialog).findByLabelText("Type web-1 to confirm")
  expect(phrase.tagName).toBe("INPUT")
  expect(phrase.getAttribute("autocomplete")).toBe("off")
  const timeout = within(dialog).getByRole("spinbutton", { name: "Revert after (seconds)" })
  const help = document.getElementById(timeout.getAttribute("aria-describedby") ?? "")
  expect(help?.textContent).toBe("Unless you confirm the change within this time, it is reverted on the host.")
  expect(timeout.getAttribute("aria-invalid")).toBe("false")
})

test("the countdown announces itself politely at fixed points, not every second", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  vi.setSystemTime(new Date("2026-10-23T08:00:00Z"))
  const pending = cdaChange({ state: "pending", revertDeadline: "2026-10-23T08:01:00Z" })
  const { dialog, user } = await cdaOpen(
    { [PLAN]: { body: cdaPlan() }, [APPLY]: { body: pending }, "GET /api/v1/changes/c1": { body: pending } },
    {},
    { advanceTimers: vi.advanceTimersByTime },
  )
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  const timer = await within(dialog).findByRole("timer")
  expect(timer.closest('[aria-live="polite"], [aria-live="assertive"], [role="alert"], [role="status"]')).toBeNull()
  const progress = within(dialog).getByRole("status", { name: "Change progress" })
  expect(progress.getAttribute("aria-live")).toBe("polite")
  expect(progress.textContent).toBe("Reverts automatically in 60 seconds unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000)
  })
  expect(timer.textContent).toBe("Reverts automatically in 55 s unless confirmed.")
  expect(progress.textContent).toBe("Reverts automatically in 60 seconds unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(25000)
  })
  expect(progress.textContent).toBe("Reverts automatically in 30 seconds unless confirmed.")
  await act(async () => {
    await vi.advanceTimersByTimeAsync(20000)
  })
  expect(progress.textContent).toBe("Reverts automatically in 10 seconds unless confirmed.")
})

test("the outcome of an apply is announced politely", async () => {
  const { dialog, user } = await cdaOpen({ [PLAN]: { body: cdaPlan() }, [APPLY]: { body: cdaChange() }, "GET /api/v1/changes/c1": { body: cdaChange() } })
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  const progress = await within(dialog).findByRole("status", { name: "Change progress" })
  expect(progress.getAttribute("aria-live")).toBe("polite")
  expect(progress.textContent).toBe("Change completed.")
})
