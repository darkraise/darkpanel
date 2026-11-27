import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test, vi } from "vitest"
import { changeQueryKey } from "@/api/client"
import type { Change } from "@/api/types"
import { sshdState } from "@/modules/m11-fixtures"
import { SshdCard } from "@/modules/users/sshd-card"
import { closedFor } from "@/modules/console/terminal"
import { ChangeActions } from "@/pages/change-actions"
import { makePlan, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"

afterEach(() => {
  vi.unstubAllGlobals()
})

const BASE = "POST /api/v1/hosts/h1/actions"

describe("A21: UX-1 the hardening form on a host whose MaxAuthTries is 20", () => {
  // The drop-in holds X11Forwarding off; sshd -T reports MaxAuthTries 20, above the form's range.
  const state = { ...sshdState, effective: { ...sshdState.effective, maxAuthTries: 20 }, set: { x11Forwarding: false } }

  function render() {
    const calls = stubFetch({
      [`${BASE}/users.sshd.get/v1/read`]: { body: { output: state } },
      "GET /api/v1/hosts/h1/modules/users/availability": { body: { module: "users", available: true, reason: null, actions: [] } },
      [`${BASE}/users.sshd.apply/v1/plan`]: { body: makePlan("users.sshd.apply", { hostId: "h1" }) },
    })
    renderWithProviders(<SshdCard hostId="h1" hostName="web-1" admin />, { hostScope: "admin" })
    return calls
  }

  test("A21: changing only Password authentication reviews the drop-in's values plus that field", async () => {
    const calls = render()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Edit hardening" }))
    await user.click(screen.getByRole("switch", { name: "Password login" }))
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByRole("button", { name: "Apply" })
    const plan = calls.find((c) => c.path.endsWith("/users.sshd.apply/v1/plan"))
    expect(plan?.body).toEqual({ input: { x11Forwarding: false, passwordAuthentication: false } })
  })

  test("A21: typing 20 into Login attempts is refused with the range text and plans nothing", async () => {
    const calls = render()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Edit hardening" }))
    const field = screen.getByLabelText("Login attempts")
    await user.clear(field)
    await user.type(field, "20")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    expect(screen.getByText("Login attempts: enter a whole number from 1 to 10")).toBeTruthy()
    expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
  })
})

describe("A31: UX-3 the terminal closed with the reason error", () => {
  test("A31: shows the toolbar state Closed, the text, and Reconnect", () => {
    expect(closedFor("error", null)).toEqual({ text: "The panel couldn't check your session", label: "Closed", reconnect: true })
  })

  test("A31: a revoked console still closes as revoked, without Reconnect", () => {
    const closed = closedFor("revoked", null)
    expect(closed.text).toBe("Your access to this host was revoked")
    expect(closed.reconnect).toBe(false)
  })
})

describe("A37: UX-2 undo of a jobs.job.delete change", () => {
  function deleted(over: Partial<Change> = {}): Change {
    return {
      id: "c9", hostId: "h1", action: "jobs.job.delete", version: 1, class: "compensable", state: "completed", actor: "user:u1",
      createdAt: "2026-11-27T08:00:00Z", updatedAt: "2026-11-27T08:05:00Z", revertDeadline: null, output: null, error: null,
      compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
    }
  }
  const stepUpRequired = {
    status: 403,
    body: { type: "about:blank", title: "auth.step_up_required", status: 403, detail: "enter an authenticator code to continue", code: "auth.step_up_required" },
  }

  function render(routes: Record<string, StubHandler>) {
    const calls = stubFetch(routes)
    const result = renderWithProviders(<ChangeActions change={deleted()} />)
    return { calls, ...result }
  }

  async function startUndo() {
    const user = userEvent.setup()
    await user.click(screen.getByRole("button", { name: "Undo" }))
    await user.click(within(screen.getByRole("dialog", { name: "Undo this change?" })).getByRole("button", { name: "Undo" }))
    return user
  }

  test("A37: without a step-up window it opens the step-up dialog, and a correct code completes the undo", async () => {
    let undos = 0
    const undoing = deleted({ state: "undoing", owner: "undo" })
    const { calls, client } = render({
      "POST /api/v1/changes/c9/undo": () => (++undos === 1 ? stepUpRequired : { status: 202, body: undoing }),
      "POST /api/v1/auth/step-up": { status: 204 },
    })
    const user = await startUndo()
    const dialog = await screen.findByRole("dialog", { name: "Enter your authenticator code" })
    await user.type(within(dialog).getByLabelText("Authenticator code"), "246810")
    await user.click(within(dialog).getByRole("button", { name: "Verify" }))
    await waitFor(() => expect(client.getQueryData(changeQueryKey("c9"))).toEqual(undoing))
    const stepUp = calls.find((c) => c.path === "/api/v1/auth/step-up")
    expect(stepUp?.body).toEqual({ code: "246810" })
    expect(calls.filter((c) => c.path === "/api/v1/changes/c9/undo")).toHaveLength(2)
  })

  test("A37: Escape cancels the step-up and leaves the change as it was", async () => {
    const { calls, client } = render({ "POST /api/v1/changes/c9/undo": stepUpRequired, "POST /api/v1/auth/step-up": { status: 204 } })
    const user = await startUndo()
    await screen.findByRole("dialog", { name: "Enter your authenticator code" })
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Enter your authenticator code" })).toBeNull())
    expect(calls.filter((c) => c.path === "/api/v1/changes/c9/undo")).toHaveLength(1)
    expect(client.getQueryData(changeQueryKey("c9"))).toBeUndefined()
  })
})
