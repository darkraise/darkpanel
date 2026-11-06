import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { fwDocker } from "@/modules/firewall/fixtures"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { npInstance } from "./fixtures"
import { InstancePanel } from "./instance-panel"
import { npmKey, type InstanceStatus } from "./types"

const IP_HOST = "/api/v1/hosts/h1"
const IP_STATUS = `POST ${IP_HOST}/actions/npm.instance.status/v1/read`
const IP_DOCKER = `GET ${IP_HOST}/resources/firewall/docker`
const IP_REVEAL = `POST ${IP_HOST}/actions/npm.admin.reveal-password/v1/reveal`
const IP_IMAGE = "jc21/nginx-proxy-manager:2.13.5@sha256:" + "a".repeat(64)
const IP_ON = "The Docker firewall integration is on: ports 80 and 443 are opened for the NPM container."
const IP_OFF = "The Docker firewall integration is off: open ports 80 and 443 to the internet yourself, then confirm that the instance is public."

function ipPlan(call: FetchCall) {
  const parts = call.path.split("/")
  return {
    body: {
      planId: "p1",
      hostId: call.path.includes("/hosts/") ? "h1" : null,
      action: parts[parts.length - 3] ?? "",
      version: 1,
      inputHash: "h",
      diff: [{ op: "~", text: "change" }],
      guards: [],
      expiresAt: "2026-11-02T13:00:00Z",
      confirmation: null,
    },
  }
}

const IP_PLANS = [
  `POST ${IP_HOST}/actions/npm.instance.confirm-public/v1/plan`,
  `POST ${IP_HOST}/actions/npm.instance.upgrade/v1/plan`,
  `POST ${IP_HOST}/actions/npm.instance.remove/v1/plan`,
  "POST /api/v1/actions/npm.instance.deploy-public/v1/plan",
  "POST /api/v1/actions/npm.admin.expose/v1/plan",
  "POST /api/v1/actions/npm.admin.unexpose/v1/plan",
]

function ipRunning(over: Partial<InstanceStatus> = {}): InstanceStatus {
  return npInstance({
    deployed: true,
    containerId: "c0ffee",
    image: IP_IMAGE,
    pinnedImage: IP_IMAGE,
    running: true,
    apiReady: true,
    public: "confirmed",
    adminEmail: "admin@example.com",
    upgradeAvailable: false,
    ...over,
  })
}

function ipNotDeployed(): InstanceStatus {
  return npInstance({ deployed: false, containerId: "", image: "", running: false, apiReady: false, public: "no", adminEmail: "" })
}

function ipRender(status: InstanceStatus, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [IP_STATUS]: { body: { output: status } },
    [IP_DOCKER]: { body: fwDocker() },
    [`GET ${IP_HOST}/modules/npm/availability`]: { body: { module: "npm", available: true, reason: null, actions: [] } },
    ...Object.fromEntries(IP_PLANS.map((key) => [key, ipPlan])),
    ...routes,
  })
  const result = renderWithProviders(<InstancePanel hostId="h1" />)
  return { calls, ...result }
}

function ipValue(label: string): string | null {
  return screen.getByText(label).nextElementSibling?.textContent ?? null
}

function ipPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

function ipPlanPath(calls: FetchCall[]): string | undefined {
  return calls.find((c) => c.method === "POST" && c.path.endsWith("/plan"))?.path
}

function ipStatusReads(calls: FetchCall[]): number {
  return calls.filter((c) => c.method === "POST" && c.path === `${IP_HOST}/actions/npm.instance.status/v1/read`).length
}

test("shows the state of a running, public instance", async () => {
  ipRender(ipRunning())
  await screen.findByText("Admin email")
  expect(ipValue("Status")).toBe("running")
  expect(ipValue("API")).toBe("ready")
  expect(ipValue("Image")).toBe(IP_IMAGE)
  expect(ipValue("Pinned image")).toBe(IP_IMAGE)
  expect(ipValue("Admin email")).toBe("admin@example.com")
  expect(ipValue("Public")).toBe("confirmed")
  expect(ipValue("Admin interface")).toBe("not exposed")
  expect(screen.queryByRole("button", { name: "Confirm public" })).toBeNull()
  expect(screen.queryByText("Upgrade available")).toBeNull()
})

test("says when the container is stopped, the API is not ready and public access is not confirmed", async () => {
  ipRender(ipRunning({ running: false, apiReady: false, public: "no", upgradeAvailable: true }))
  await screen.findByText("Admin email")
  expect(ipValue("Status")).toBe("stopped")
  expect(ipValue("API")).toBe("not ready")
  expect(ipValue("Public")).toBe("not confirmed")
  expect(screen.getByText("Upgrade available")).toBeTruthy()
  expect(screen.getByRole("button", { name: "Confirm public" })).toBeTruthy()
})

test.each([
  ["on", fwDocker({ enabled: true, supported: true }), true, IP_ON],
  ["off", fwDocker({ enabled: false, supported: true }), false, IP_OFF],
  ["unsupported", fwDocker({ enabled: true, supported: false, unsupported: ["rules"] }), false, IP_OFF],
])("deploys with the integration flag read from firewall.docker when it is %s", async (_, docker, integration, hint) => {
  const { calls } = ipRender(ipNotDeployed(), { [IP_DOCKER]: { body: docker } })
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Deploy" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await within(form).findByText(hint)
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Let's Encrypt email"), " ops@example.com ")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: "Deploy Nginx Proxy Manager" })
  await within(dialog).findByLabelText("Planned changes")
  expect(ipPlanPath(calls)).toBe("/api/v1/actions/npm.instance.deploy-public/v1/plan")
  expect(ipPlanBodies(calls)).toEqual([{ input: { hostId: "h1", acmeEmail: "ops@example.com", integration } }])
})

test("keeps Plan off and shows the reason while firewall.docker cannot be read", async () => {
  ipRender(ipNotDeployed(), {
    [IP_DOCKER]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the firewall module is unavailable", code: "module.unavailable" } },
  })
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Deploy" })
  await user.type(within(form).getByLabelText("Let's Encrypt email"), "ops@example.com")
  expect((await within(form).findByRole("alert")).textContent).toBe("the firewall module is unavailable")
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})

test("Confirm public plans npm.instance.confirm-public on the host", async () => {
  const { calls } = ipRender(ipRunning({ public: "no" }))
  await userEvent.setup().click(await screen.findByRole("button", { name: "Confirm public" }))
  const dialog = await screen.findByRole("dialog", { name: "Confirm that the instance is public" })
  await within(dialog).findByLabelText("Planned changes")
  expect(ipPlanPath(calls)).toBe(`${IP_HOST}/actions/npm.instance.confirm-public/v1/plan`)
  expect(ipPlanBodies(calls)).toEqual([{ input: {} }])
})

test.each([
  ["Upgrade", "npm.instance.upgrade", "Upgrade Nginx Proxy Manager"],
  ["Remove instance", "npm.instance.remove", "Remove Nginx Proxy Manager"],
])("%s plans %s on the host with an empty input", async (button, action, title) => {
  const { calls } = ipRender(ipRunning())
  await userEvent.setup().click(await screen.findByRole("button", { name: button }))
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  expect(ipPlanPath(calls)).toBe(`${IP_HOST}/actions/${action}/v1/plan`)
  expect(ipPlanBodies(calls)).toEqual([{ input: {} }])
})

test.each([
  ["", { hostId: "h1", hostname: "npm.example.com" }],
  ["7", { hostId: "h1", hostname: "npm.example.com", accessListId: 7 }],
])("exposing the admin interface with access list %j plans npm.admin.expose on the controller", async (accessList, input) => {
  const { calls } = ipRender(ipRunning())
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Expose admin interface" }))
  const form = await screen.findByRole("form", { name: "Admin exposure" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Hostname"), " NPM.Example.com ")
  expect(plan.hasAttribute("disabled")).toBe(false)
  if (accessList !== "") await user.type(within(form).getByLabelText("Access list ID (optional)"), accessList)
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: "Expose the admin interface at npm.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(ipPlanPath(calls)).toBe("/api/v1/actions/npm.admin.expose/v1/plan")
  expect(ipPlanBodies(calls)).toEqual([{ input }])
})

test.each(["abc", "0", "-3"])("the access list ID %j keeps Plan off", async (typed) => {
  ipRender(ipRunning())
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Expose admin interface" }))
  const form = await screen.findByRole("form", { name: "Admin exposure" })
  await user.type(within(form).getByLabelText("Hostname"), "npm.example.com")
  await user.type(within(form).getByLabelText("Access list ID (optional)"), typed)
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})

test("an exposed admin interface is shown and removed through npm.admin.unexpose, which reads the status again", async () => {
  const exposed = ipRunning({ adminExposure: { hostname: "npm.example.com", proxyHostId: 4, certificateId: 5 } })
  const { calls } = ipRender(exposed)
  const user = userEvent.setup()
  await screen.findByText("Admin email")
  expect(ipValue("Admin interface")).toBe("https://npm.example.com")
  expect(screen.queryByRole("button", { name: "Expose admin interface" })).toBeNull()
  await user.click(screen.getByRole("button", { name: "Remove admin exposure" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove the admin exposure at npm.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(ipPlanPath(calls)).toBe("/api/v1/actions/npm.admin.unexpose/v1/plan")
  expect(ipPlanBodies(calls)).toEqual([{ input: { hostId: "h1", hostname: "npm.example.com" } }])
  expect(ipStatusReads(calls)).toBe(1)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(ipStatusReads(calls)).toBe(2))
})

test("reveals the admin password once, in a dialog, and never caches it", async () => {
  const { calls, client } = ipRender(ipRunning(), {
    [IP_REVEAL]: { body: { value: { email: "admin@example.com", password: "s3cret-Pa55" } } },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Reveal admin password" }))
  const dialog = await screen.findByRole("dialog", { name: "NPM admin credentials" })
  expect(within(dialog).getByText("Password").nextElementSibling?.textContent).toBe("s3cret-Pa55")
  expect(within(dialog).getByText("Email").nextElementSibling?.textContent).toBe("admin@example.com")
  const reveals = () => calls.filter((c) => c.path.endsWith("/reveal")).map((c) => [c.method, c.path, c.body])
  expect(reveals()).toEqual([["POST", `${IP_HOST}/actions/npm.admin.reveal-password/v1/reveal`, { input: {} }]])
  expect(JSON.stringify(client.getQueryCache().getAll().map((q) => q.state.data))).not.toContain("s3cret-Pa55")
  await user.click(within(dialog).getByRole("button", { name: "Hide password" }))
  await waitFor(() => expect(screen.queryByText("s3cret-Pa55")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Reveal admin password" }))
  expect(await screen.findByText("s3cret-Pa55")).toBeTruthy()
  expect(reveals()).toHaveLength(2)
})

test("shows why the password could not be revealed", async () => {
  ipRender(ipRunning(), {
    [IP_REVEAL]: { status: 403, body: { type: "about:blank", title: "Forbidden", status: 403, detail: "this request needs the admin scope", code: "auth.forbidden" } },
  })
  await userEvent.setup().click(await screen.findByRole("button", { name: "Reveal admin password" }))
  expect((await screen.findByRole("alert")).textContent).toBe("this request needs the admin scope")
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("shows the reason when the status cannot be read", async () => {
  ipRender(ipRunning(), {
    [IP_STATUS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the NPM module is unavailable", code: "module.unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the NPM module is unavailable")
})

test("a failed background refetch keeps the panel and an open change dialog", async () => {
  let reads = 0
  const { client } = ipRender(ipRunning(), {
    [IP_STATUS]: () => {
      reads += 1
      if (reads === 1) return { body: { output: ipRunning() } }
      return { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the NPM module is unavailable", code: "module.unavailable" } }
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Upgrade" }))
  const dialog = await screen.findByRole("dialog", { name: "Upgrade Nginx Proxy Manager" })
  await within(dialog).findByLabelText("Planned changes")
  await client.invalidateQueries({ queryKey: npmKey("h1", "instance.status") })
  await waitFor(() => expect(client.getQueryState(npmKey("h1", "instance.status"))?.status).toBe("error"))
  expect(screen.getByRole("dialog", { name: "Upgrade Nginx Proxy Manager" })).toBeTruthy()
  expect(screen.getByText("Admin email")).toBeTruthy()
  expect(screen.queryByText("the NPM module is unavailable")).toBeNull()
})

test("shows an error when the host answers with something that is not an instance status", async () => {
  ipRender(ipRunning(), { [IP_STATUS]: { body: { output: { deployed: "yes" } } } })
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected NPM instance status.")
})

test("the panel has no accessibility violations", async () => {
  const { container } = ipRender(ipRunning({ public: "no" }))
  await screen.findByText("Admin email")
  expect(await axe(container)).toHaveNoViolations()
})
