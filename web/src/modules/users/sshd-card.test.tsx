import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test, vi } from "vitest"
import { sshdState } from "@/modules/m11-fixtures"
import { makeChange, makePlan, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { OPEN_A_SESSION, SshdCard } from "./sshd-card"

const BASE = "POST /api/v1/hosts/h1/actions"
const GET = `${BASE}/users.sshd.get/v1/read`
const PLAN = `${BASE}/users.sshd.apply/v1/plan`
const APPLY = `${BASE}/users.sshd.apply/v1/apply`
const AVAILABILITY = "GET /api/v1/hosts/h1/modules/users/availability"
const ELSEWHERE = "/etc/ssh/sshd_config.d/50-cloud-init.conf"
const NO_WAY_IN = "After the change no account could sign in over SSH. Considered: root (no password login), deploy (no key)."

function render(extra: Record<string, StubHandler> = {}, state: unknown = sshdState) {
  const calls = stubFetch({
    [GET]: { body: { output: state } },
    [AVAILABILITY]: { body: { module: "users", available: true, reason: null, actions: [] } },
    ...extra,
  })
  renderWithProviders(<SshdCard hostId="h1" hostName="web-1" admin />, { hostScope: "admin" })
  return calls
}

async function openForm() {
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Edit hardening" }))
  return user
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("hardening flow", () => {
  test("the card lists the effective values", async () => {
    render()
    expect(await screen.findByText("Keys only")).toBeTruthy()
    expect(screen.getByText("120 s")).toBeTruthy()
    expect(screen.getByText("Everyone")).toBeTruthy()
  })

  test("the form starts from effective values", async () => {
    render()
    await openForm()
    expect((screen.getByRole("switch", { name: "Password login" }) as HTMLButtonElement).getAttribute("aria-checked")).toBe("true")
    expect((screen.getByLabelText("Login attempts") as HTMLInputElement).value).toBe("6")
    expect((screen.getByLabelText("Login grace time (seconds)") as HTMLInputElement).value).toBe("120")
    expect((screen.getByLabelText("Root login") as HTMLSelectElement).value).toBe("prohibit-password")
  })

  test("apply opens ChangeDialog with the countdown and the confirm step text", async () => {
    const calls = render({
      [PLAN]: { body: makePlan("users.sshd.apply", { hostId: "h1" }) },
      [APPLY]: { body: makeChange("users.sshd.apply", null, { hostId: "h1", class: "revertible", state: "pending", revertDeadline: new Date(Date.now() + 60_000).toISOString() }) },
    })
    const user = await openForm()
    await user.click(screen.getByRole("switch", { name: "Password login" }))
    await user.click(screen.getByRole("button", { name: "Review change" }))
    expect(await screen.findByText(OPEN_A_SESSION)).toBeTruthy()
    expect(screen.getByLabelText("Revert after (seconds)")).toBeTruthy()
    await user.click(await screen.findByRole("button", { name: "Apply" }))
    expect(await screen.findByRole("timer")).toBeTruthy()
    expect(screen.getByRole("timer").textContent).toMatch(/^Reverts automatically in \d+ s unless confirmed\.$/)
    expect(screen.getByText(OPEN_A_SESSION)).toBeTruthy()
    expect(screen.getByRole("button", { name: "Confirm" })).toBeTruthy()
    const plan = calls.find((c) => c.path.endsWith("users.sshd.apply/v1/plan"))
    expect(plan?.body).toEqual({ input: { passwordAuthentication: false } })
    const apply = calls.find((c) => c.path.endsWith("users.sshd.apply/v1/apply"))
    expect(apply?.body).toMatchObject({ confirmTimeoutSeconds: 60 })
  })

  test("a users.no_way_in guard shows an error naming the accounts and an override check box", async () => {
    const plans: unknown[] = []
    render({
      [PLAN]: (call) => {
        plans.push(call.body)
        const overridden = JSON.stringify(call.body).includes("overrides")
        return {
          body: makePlan("users.sshd.apply", {
            hostId: "h1",
            guards: [{ id: "users.no_way_in", ok: overridden, overridden, overridable: true, message: NO_WAY_IN }],
          }),
        }
      },
    })
    const user = await openForm()
    await user.click(screen.getByRole("switch", { name: "Password login" }))
    await user.click(screen.getByRole("button", { name: "Review change" }))
    const guards = await screen.findByRole("list", { name: "Guards" })
    expect(within(guards).getByText("failed")).toBeTruthy()
    expect(within(guards).getByText(NO_WAY_IN)).toBeTruthy()
    expect((screen.getByRole("button", { name: "Apply" }) as HTMLButtonElement).disabled).toBe(true)
    await user.click(screen.getByRole("checkbox", { name: "Override users.no_way_in" }))
    expect(await within(guards).findByText("overridden")).toBeTruthy()
    expect(plans[1]).toEqual({ input: { passwordAuthentication: false, overrides: ["users.no_way_in"] } })
  })

  test("AllowUsers set elsewhere shows read-only with the file name and is left out", async () => {
    const state = { ...sshdState, effective: { ...sshdState.effective, allowUsers: ["deploy"] }, readOnly: ["allowUsers", "allowGroups"], listsSetBy: ELSEWHERE }
    const calls = render({ [PLAN]: { body: makePlan("users.sshd.apply", { hostId: "h1" }) } }, state)
    expect(await screen.findByText(`deploy (set in ${ELSEWHERE})`)).toBeTruthy()
    const user = await openForm()
    const field = screen.getByLabelText("Allowed users") as HTMLInputElement
    expect(field.readOnly).toBe(true)
    expect(field.value).toBe("deploy")
    expect(screen.getAllByText(`Set in ${ELSEWHERE}; edit that file to change it`)).toHaveLength(2)
    await user.clear(screen.getByLabelText("Login attempts"))
    await user.type(screen.getByLabelText("Login attempts"), "3")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByRole("button", { name: "Apply" })
    expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { maxAuthTries: 3 } })
  })

  test("lists Darkpanel set before another file took them over are dropped from the change", async () => {
    const state = {
      ...sshdState,
      set: { ...sshdState.set, allowUsers: ["deploy"] },
      effective: { ...sshdState.effective, allowUsers: ["deploy"] },
      readOnly: ["allowUsers", "allowGroups"],
      listsSetBy: ELSEWHERE,
    }
    const calls = render({ [PLAN]: { body: makePlan("users.sshd.apply", { hostId: "h1" }) } }, state)
    const user = await openForm()
    await user.clear(screen.getByLabelText("Login attempts"))
    await user.type(screen.getByLabelText("Login attempts"), "3")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByRole("button", { name: "Apply" })
    const input = (calls.find((c) => c.path.endsWith("/plan"))?.body as { input: Record<string, unknown> }).input
    expect(input).not.toHaveProperty("allowUsers")
    expect(input.maxAuthTries).toBe(3)
  })

  test("a value out of range is refused under its field before review", async () => {
    render()
    const user = await openForm()
    await user.clear(screen.getByLabelText("Login attempts"))
    await user.type(screen.getByLabelText("Login attempts"), "40")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    const field = screen.getByLabelText("Login attempts")
    expect(field.getAttribute("aria-invalid")).toBe("true")
    expect(document.getElementById(field.getAttribute("aria-describedby")?.split(" ")[0] ?? "")?.textContent).toBe(
      "Login attempts: enter a whole number from 1 to 10",
    )
  })

  test("reviews only edited fields", async () => {
    const state = { ...sshdState, effective: { ...sshdState.effective, maxAuthTries: 20, permitRootLogin: "forced-commands-only" }, set: { x11Forwarding: false } }
    const calls = render({ [PLAN]: { body: makePlan("users.sshd.apply", { hostId: "h1" }) } }, state)
    expect(await screen.findByText("Forced commands only")).toBeTruthy()
    const user = await openForm()
    const root = screen.getByLabelText("Root login") as HTMLSelectElement
    expect(root.value).toBe("forced-commands-only")
    expect(root.selectedOptions[0]?.textContent).toBe("Forced commands only")
    await user.click(screen.getByRole("switch", { name: "Password login" }))
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByRole("button", { name: "Apply" })
    expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { x11Forwarding: false, passwordAuthentication: false } })
  })

  test("an edited number is checked even when it equals the effective value", async () => {
    const calls = render({}, { ...sshdState, effective: { ...sshdState.effective, maxAuthTries: 20 } })
    const user = await openForm()
    await user.clear(screen.getByLabelText("Login attempts"))
    await user.type(screen.getByLabelText("Login attempts"), "20")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    expect(screen.getByLabelText("Login attempts").getAttribute("aria-invalid")).toBe("true")
    expect(screen.getByText("Login attempts: enter a whole number from 1 to 10")).toBeTruthy()
    expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
  })

  test("the form names the host, and a refused plan returns to it as typed", async () => {
    render({ [PLAN]: { status: 422, body: { type: "about:blank", title: "Invalid", status: 422, detail: "refused", code: "invalid_input" } } })
    const user = await openForm()
    expect(screen.getByRole("dialog", { name: "Edit SSH hardening on web-1" })).toBeTruthy()
    await user.clear(screen.getByLabelText("Login attempts"))
    await user.type(screen.getByLabelText("Login attempts"), "4")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByText("refused")
    await user.click(within(screen.getByRole("dialog", { name: "Apply SSH hardening" })).getAllByRole("button", { name: "Close" })[0] as HTMLElement)
    expect(((await screen.findByLabelText("Login attempts")) as HTMLInputElement).value).toBe("4")
  })
})
