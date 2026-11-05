import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AllowAction, DenyAction } from "./container-actions"
import { fwBinding, fwDocker } from "./fixtures"
import type { DockerStatus } from "./types"

const CA_DOCKER = "GET /api/v1/hosts/h1/resources/firewall/docker"
const CA_PENDING = { id: "ch1", state: "pending", class: "revertible", error: null, output: null, revertDeadline: "2099-01-01T00:00:00Z" }

const caWebPort = { privatePort: 80, protocol: "tcp" as const, bindings: [{ hostIp: "0.0.0.0", hostPort: 8080 }] }
const caContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [caWebPort] }

// caDocker is a healthy, supported integration whatever fwDocker's defaults are.
function caDocker(over: Partial<DockerStatus> = {}): DockerStatus {
  return fwDocker({ available: true, supported: true, unsupported: [], enabled: true, bindings: [], ...over })
}

function caAllowedWeb(allowed: boolean): DockerStatus {
  return caDocker({ bindings: [fwBinding({ binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containers: ["web"], allowed: allowed ? ["any"] : [], denied: [] })] })
}

function caRender(kind: "allow" | "deny", read: () => DockerStatus, container: ContainerRef = caContainer, routes: Record<string, StubHandler> = {}) {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "+", text: action }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null } }
  }
  const calls = stubFetch({
    [CA_DOCKER]: () => ({ body: read() }),
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.container.allow/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.container.deny/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.container.allow/v1/apply": { body: CA_PENDING },
    "POST /api/v1/hosts/h1/actions/firewall.container.deny/v1/apply": { body: CA_PENDING },
    "GET /api/v1/changes/ch1": { body: CA_PENDING },
    "POST /api/v1/changes/ch1/confirm": { body: { ...CA_PENDING, state: "completed", revertDeadline: null } },
    ...routes,
  })
  const view = kind === "allow" ? <AllowAction container={container} onDone={vi.fn()} /> : <DenyAction container={container} onDone={vi.fn()} />
  return { dockerReads: () => calls.filter((c) => c.method === "GET" && c.path === "/api/v1/hosts/h1/resources/firewall/docker").length, ...renderWithProviders(view) }
}

async function caPlan(user: ReturnType<typeof userEvent.setup>, kind: "Allow" | "Deny") {
  const form = await screen.findByRole("form", { name: kind })
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  return dialog
}

test.each([
  { state: "the Allow form", kind: "allow" as const, docker: caAllowedWeb(false), container: caContainer, form: "Allow" },
  { state: "the Deny form", kind: "deny" as const, docker: caAllowedWeb(true), container: caContainer, form: "Deny" },
  { state: "an Allow form for a container without published ports", kind: "allow" as const, docker: caDocker(), container: { ...caContainer, ports: [] }, form: "Allow" },
  { state: "a Deny form with no allowed binding", kind: "deny" as const, docker: caDocker(), container: caContainer, form: "Deny" },
  { state: "an unsupported integration", kind: "allow" as const, docker: caDocker({ supported: false, unsupported: ["Docker 27 is older than 28.0"] }), container: caContainer, form: "Allow" },
])("$state has no axe violations", async ({ kind, docker, container, form }) => {
  const { container: root } = caRender(kind, () => docker, container)
  await screen.findByRole("form", { name: form })
  await expectNoAxeViolations(root)
})

test("a failed read of the integration has no axe violations", async () => {
  const { container } = caRender("allow", () => caDocker(), caContainer, {
    [CA_DOCKER]: { status: 500, body: { type: "about:blank", title: "Error", status: 500, detail: "the agent is unreachable", code: "internal" } },
  })
  await screen.findByRole("alert")
  await expectNoAxeViolations(container)
})

test("the plan dialog and the pending change dialog have no axe violations", async () => {
  caRender("allow", () => caAllowedWeb(false))
  const user = userEvent.setup()
  const dialog = await caPlan(user, "Allow")
  await expectNoAxeViolations(dialog)
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByRole("timer")
  await expectNoAxeViolations(dialog)
})

test("Tab goes from the binding to the source to Plan, each with a visible name", async () => {
  caRender("allow", () => caAllowedWeb(false))
  await screen.findByRole("form", { name: "Allow" })
  expect(await tabSequence(userEvent.setup(), 3)).toEqual(["all:8080/tcp", "From", "Plan"])
})

test.each(["allow", "deny"] as const)("the %s dialog keeps Tab inside it and Escape gives focus back to Plan after the form re-renders", async (kind) => {
  const { dockerReads } = caRender(kind, () => caAllowedWeb(true))
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: kind === "allow" ? "Allow" : "Deny" })
  if (kind === "deny") await user.type(within(form).getByLabelText("From"), "203.0.113.0/24")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(dialog.contains(document.activeElement)).toBe(true)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  const before = dockerReads()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(dockerReads()).toBeGreaterThan(before))
  expect(document.activeElement).toBe(within(screen.getByRole("form", { name: kind === "allow" ? "Allow" : "Deny" })).getByRole("button", { name: "Plan" }))
})

test("a read that lands after the close's read failed leaves focus where the user put it", async () => {
  let fail = false
  const down = { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the agent is offline", code: "host.offline" } }
  const { client } = caRender("allow", () => caAllowedWeb(false), caContainer, {
    [CA_DOCKER]: () => (fail ? down : { body: caAllowedWeb(false) }),
  })
  const outside = document.createElement("button")
  outside.textContent = "Elsewhere"
  document.body.append(outside)
  try {
    const user = userEvent.setup()
    await caPlan(user, "Allow")
    fail = true
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await screen.findByRole("alert")
    outside.focus()
    fail = false
    await act(() => client.invalidateQueries())
    await screen.findByRole("form", { name: "Allow" })
    expect(document.activeElement).toBe(outside)
  } finally {
    outside.remove()
  }
})

test("when a confirmed Deny leaves nothing to deny, the Plan button is disabled and focus moves to the form", async () => {
  let allowed = true
  caRender("deny", () => caAllowedWeb(allowed))
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Deny" })
  await user.type(within(form).getByLabelText("From"), "203.0.113.0/24")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  allowed = false
  await user.click(apply)
  await user.click(await within(dialog).findByRole("button", { name: "Confirm" }))
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect((await screen.findByText("web has no allowed binding. Allow one first.")).textContent).toBe("web has no allowed binding. Allow one first.")
  expect(within(screen.getByRole("form", { name: "Deny" })).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("form", { name: "Deny" })))
  expect(focusedName()).toBe("Deny")
})

test("when the read after a Deny has already disabled Plan, focus is on the form as soon as the dialog closes", async () => {
  let allowed = true
  let hold: Promise<void> | null = null
  let release = () => {}
  caRender("deny", () => caAllowedWeb(allowed), caContainer, {
    [CA_DOCKER]: async () => {
      if (hold !== null) await hold
      return { body: caAllowedWeb(allowed) }
    },
  })
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Deny" })
  await user.type(within(form).getByLabelText("From"), "203.0.113.0/24")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  allowed = false
  await user.click(apply)
  await user.click(await within(dialog).findByRole("button", { name: "Confirm" }))
  await within(dialog).findByText("completed")
  await screen.findByText("web has no allowed binding. Allow one first.")
  expect(within(form).getByRole("button", { name: "Plan", hidden: true }).hasAttribute("disabled")).toBe(true)
  hold = new Promise((resolve) => {
    release = resolve
  })
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("form", { name: "Deny" }))
  release()
})
