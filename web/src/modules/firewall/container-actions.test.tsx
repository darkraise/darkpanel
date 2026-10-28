import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { ContainerRef } from "@/modules/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AllowAction, DenyAction } from "./container-actions"
import { fwBinding, fwDocker } from "./fixtures"
import type { DockerStatus } from "./types"

const CA_DOCKER = "GET /api/v1/hosts/h1/resources/firewall/docker"

// caDocker is a healthy, supported integration whatever fwDocker's defaults are.
function caDocker(over: Partial<DockerStatus> = {}): DockerStatus {
  return fwDocker({ available: true, supported: true, unsupported: [], enabled: true, bindings: [], ...over })
}

function caContainer(over: Partial<ContainerRef> = {}): ContainerRef {
  return {
    hostId: "h1",
    containerId: "c1",
    name: "web",
    ports: [{ privatePort: 80, protocol: "tcp", bindings: [{ hostIp: "0.0.0.0", hostPort: 8080 }, { hostIp: "::", hostPort: 8080 }] }],
    ...over,
  }
}

function caRender(kind: "allow" | "deny", docker: DockerStatus, container: ContainerRef = caContainer(), routes: Record<string, StubHandler> = {}) {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "+", text: action }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null } }
  }
  const calls = stubFetch({
    [CA_DOCKER]: { body: docker },
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/firewall.container.allow/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/firewall.container.deny/v1/plan": plan,
    ...routes,
  })
  const onDone = vi.fn()
  const view = kind === "allow" ? <AllowAction container={container} onDone={onDone} /> : <DenyAction container={container} onDone={onDone} />
  const result = renderWithProviders(view)
  return { calls, onDone, ...result }
}

function caPlanCall(calls: FetchCall[]): { path: string; body: unknown } {
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

const caWeb = {
  all: fwBinding({ binding: { selector: "web", hostPort: 8080, proto: "tcp" }, containers: ["web"], allowed: [], denied: [] }),
  local: fwBinding({ binding: { selector: "web", hostAddr: "127.0.0.1", hostPort: 8081, proto: "tcp" }, containers: ["web"], allowed: [], denied: [] }),
  other: fwBinding({ binding: { selector: "db", hostPort: 5432, proto: "tcp" }, containers: ["db"], allowed: [], denied: [] }),
}

const caLocalPort = { privatePort: 80, protocol: "tcp" as const, bindings: [{ hostIp: "0.0.0.0", hostPort: 8080 }, { hostIp: "127.0.0.1", hostPort: 8081 }] }

test("Allow lists the container's own bindings and plans an allowance from any source", async () => {
  const { calls } = caRender("allow", caDocker({ bindings: [caWeb.all, caWeb.local, caWeb.other] }), caContainer({ ports: [caLocalPort] }))
  const form = await screen.findByRole("form", { name: "Allow" })
  expect(within(form).getAllByRole("radio").map((r) => r.getAttribute("id") && form.querySelector(`label[for="${r.id}"]`)?.textContent)).toEqual(["all:8080/tcp", "127.0.0.1:8081/tcp"])
  expect(within(form).getByRole("radio", { name: "all:8080/tcp", checked: true })).toBeTruthy()
  await userEvent.setup().click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Allow any source to web on all:8080/tcp" })
  await within(dialog).findByLabelText("Planned changes")
  expect(caPlanCall(calls)).toEqual({
    path: "/api/v1/hosts/h1/actions/firewall.container.allow/v1/plan",
    body: { input: { binding: { selector: "web", hostPort: 8080, proto: "tcp" } } },
  })
})

test("Allow with a source plans the chosen binding and the source", async () => {
  const { calls } = caRender("allow", caDocker({ bindings: [caWeb.all, caWeb.local] }), caContainer({ ports: [caLocalPort] }))
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Allow" })
  await user.click(within(form).getByRole("radio", { name: "127.0.0.1:8081/tcp" }))
  await user.type(within(form).getByLabelText("From"), " 203.0.113.0/24 ")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog", { name: "Allow 203.0.113.0/24 to web on 127.0.0.1:8081/tcp" })).findByLabelText("Planned changes")
  expect(caPlanCall(calls).body).toEqual({
    input: { binding: { selector: "web", hostAddr: "127.0.0.1", hostPort: 8081, proto: "tcp" }, from: "203.0.113.0/24" },
  })
})

test("a published port the integration has no binding for yet is offered, once, as a binding on every address", async () => {
  const container = caContainer({
    ports: [
      { privatePort: 80, protocol: "tcp", bindings: [{ hostIp: "0.0.0.0", hostPort: 9090 }, { hostIp: "::", hostPort: 9090 }] },
      { privatePort: 53, protocol: "udp", bindings: [{ hostIp: "::1", hostPort: 5353 }] },
      { privatePort: 443, protocol: "tcp", bindings: [] },
    ],
  })
  const { calls } = caRender("allow", caDocker({ bindings: [] }), container)
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Allow" })
  expect(within(form).getAllByRole("radio")).toHaveLength(2)
  expect(within(form).getByRole("radio", { name: "all:9090/tcp" })).toBeTruthy()
  await user.click(within(form).getByRole("radio", { name: "[::1]:5353/udp" }))
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog", { name: "Allow any source to web on [::1]:5353/udp" })).findByLabelText("Planned changes")
  expect(caPlanCall(calls).body).toEqual({ input: { binding: { selector: "web", hostAddr: "::1", hostPort: 5353, proto: "udp" } } })
})

test("a binding the integration already knows keeps its stored selector", async () => {
  const shop = fwBinding({ binding: { selector: "shop/web", hostPort: 8080, proto: "tcp" }, containers: ["web"], allowed: [], denied: [] })
  const { calls } = caRender("allow", caDocker({ bindings: [shop] }))
  const form = await screen.findByRole("form", { name: "Allow" })
  expect(within(form).getAllByRole("radio")).toHaveLength(1)
  await userEvent.setup().click(within(form).getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog")).findByLabelText("Planned changes")
  expect(caPlanCall(calls).body).toEqual({ input: { binding: { selector: "shop/web", hostPort: 8080, proto: "tcp" } } })
})

test("Deny offers only allowed bindings and needs a source", async () => {
  const allowed = { ...caWeb.all, allowed: ["any"] }
  const { calls } = caRender("deny", caDocker({ bindings: [allowed, caWeb.local] }), caContainer({ ports: [caLocalPort] }))
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Deny" })
  expect(within(form).getAllByRole("radio")).toHaveLength(1)
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("From"), "203.0.113.0/24")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.click(plan)
  await within(await screen.findByRole("dialog", { name: "Deny 203.0.113.0/24 to web on all:8080/tcp" })).findByLabelText("Planned changes")
  expect(caPlanCall(calls)).toEqual({
    path: "/api/v1/hosts/h1/actions/firewall.container.deny/v1/plan",
    body: { input: { binding: { selector: "web", hostPort: 8080, proto: "tcp" }, from: "203.0.113.0/24" } },
  })
})

test("Deny with no allowed binding says to allow one first", async () => {
  caRender("deny", caDocker({ bindings: [caWeb.all] }))
  const form = await screen.findByRole("form", { name: "Deny" })
  expect(within(form).getByRole("status").textContent).toBe("web has no allowed binding. Allow one first.")
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})

test("an unavailable Docker disables the action with Docker's reason", async () => {
  const { calls } = caRender("allow", caDocker({ available: false, reason: "the Docker socket is not reachable", bindings: [] }))
  const form = await screen.findByRole("form", { name: "Allow" })
  expect(within(form).getByRole("status").textContent).toBe("the Docker socket is not reachable")
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await userEvent.setup().click(plan)
  expect(calls.some((c) => c.method === "POST")).toBe(false)
})

test("an unsupported integration disables the action with every reason", async () => {
  caRender("deny", caDocker({ supported: false, unsupported: ["Docker 27.5 is older than 28.0", "FirewallBackend.Driver is nftables"], bindings: [{ ...caWeb.all, allowed: ["any"] }] }))
  const form = await screen.findByRole("form", { name: "Deny" })
  expect(within(form).getByRole("status").textContent).toBe(
    "The Docker integration is not supported: Docker 27.5 is older than 28.0; FirewallBackend.Driver is nftables",
  )
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})

test("a container that publishes nothing has nothing to allow", async () => {
  caRender("allow", caDocker({ bindings: [caWeb.other] }), caContainer({ ports: [] }))
  const form = await screen.findByRole("form", { name: "Allow" })
  expect(within(form).getByRole("status").textContent).toBe("web publishes no ports.")
  expect(within(form).queryAllByRole("radio")).toHaveLength(0)
})

test("onDone runs when the change dialog closes", async () => {
  const { onDone } = caRender("allow", caDocker({ bindings: [caWeb.all] }))
  const user = userEvent.setup()
  await user.click(within(await screen.findByRole("form", { name: "Allow" })).getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog")).findByLabelText("Planned changes")
  expect(onDone).not.toHaveBeenCalled()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(onDone).toHaveBeenCalledTimes(1))
})

test("a failed Docker read shows the server's reason", async () => {
  caRender("allow", caDocker(), caContainer(), {
    [CA_DOCKER]: { status: 409, body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the firewall module is unavailable on this host", code: "module.unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the firewall module is unavailable on this host")
  expect(screen.queryByRole("form")).toBeNull()
})

test("the Allow and Deny forms have no accessibility violations", async () => {
  const { container } = caRender("allow", caDocker({ bindings: [{ ...caWeb.all, allowed: ["any"] }, caWeb.local] }), caContainer({ ports: [caLocalPort] }))
  await screen.findByRole("form", { name: "Allow" })
  expect(await axe(container)).toHaveNoViolations()
})
