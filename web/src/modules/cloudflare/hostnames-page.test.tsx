import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { cfHostname, cfTunnel } from "./fixtures"
import { HostnamesPage } from "./hostnames-page"
import type { HostnameView, TunnelView } from "./types"

const TUNNELS = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"
const HOSTNAMES = "POST /api/v1/actions/cloudflare.hostname.list/v1/read"

function hpHostnames(): HostnameView[] {
  return [
    cfHostname({ hostname: "app.example.com", tunnel: "t1", service: "http://web:8080", phase: "active", health: "ok", checkedAt: "2026-10-30T08:10:00Z" }),
    cfHostname({ hostname: "git.example.com", tunnel: "t2", service: "https://git:3000", phase: "active", health: "rule_missing" }),
    cfHostname({ hostname: "new.example.com", tunnel: "t9", service: "tcp://db:5432", phase: "adding_record", health: "unknown" }),
  ]
}

function hpTunnels(): TunnelView[] {
  return [cfTunnel({ id: "t1", name: "darkpanel-c1" }), cfTunnel({ id: "t2", name: "darkpanel-c2" })]
}

function hpPlan(action: string): Plan {
  return { planId: "p1", hostId: null, action, version: 1, inputHash: "h", diff: [{ op: "+", text: "rule" }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null }
}

function hpRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [TUNNELS]: { body: { output: { tunnels: hpTunnels() } } },
    [HOSTNAMES]: { body: { output: { hostnames: hpHostnames() } } },
    "POST /api/v1/actions/cloudflare.hostname.add/v1/plan": { body: hpPlan("cloudflare.hostname.add") },
    "POST /api/v1/actions/cloudflare.hostname.remove/v1/plan": { body: hpPlan("cloudflare.hostname.remove") },
    ...routes,
  })
  const result = renderWithProviders(<HostnamesPage />)
  return { calls, ...result }
}

async function hpTable() {
  return screen.findByRole("table", { name: "Hostnames" })
}

function hpForm(): HTMLElement {
  return screen.getByRole("form", { name: "Add hostname" })
}

function hpPlanCalls(calls: FetchCall[]): FetchCall[] {
  return calls.filter((c) => c.path.endsWith("/plan"))
}

test("lists the hostnames with their tunnel, service, state and health, in the server's order", async () => {
  hpRender()
  const rows = within(await hpTable()).getAllByRole("row").slice(1)
  expect(rows.map((r) => within(r).getAllByRole("cell").slice(0, 5).map((c) => c.textContent))).toEqual([
    ["app.example.com", "darkpanel-c1", "http://web:8080", "active", "ok"],
    ["git.example.com", "darkpanel-c2", "https://git:3000", "active", "rule missing"],
    ["new.example.com", "t9", "tcp://db:5432", "adding record", "unknown"],
  ])
})

test("removing a hostname plans the removal by hostname", async () => {
  const { calls } = hpRender()
  const user = userEvent.setup()
  await hpTable()
  await user.click(screen.getByRole("button", { name: "Remove hostname git.example.com" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove git.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(hpPlanCalls(calls).map((c) => [c.path, c.body])).toEqual([["/api/v1/actions/cloudflare.hostname.remove/v1/plan", { input: { hostname: "git.example.com" } }]])
})

test("adds a hostname to the chosen tunnel and plans it with the trimmed text", async () => {
  const { calls } = hpRender()
  const user = userEvent.setup()
  await hpTable()
  const form = hpForm()
  await user.click(within(form).getByRole("radio", { name: "darkpanel-c2" }))
  await user.type(within(form).getByLabelText("Hostname"), "  blog.example.com ")
  await user.type(within(form).getByLabelText("Service"), "http://blog:2368 ")
  await user.click(within(form).getByRole("button", { name: "Add hostname" }))
  const dialog = await screen.findByRole("dialog", { name: "Add blog.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(hpPlanCalls(calls).map((c) => [c.path, c.body])).toEqual([
    ["/api/v1/actions/cloudflare.hostname.add/v1/plan", { input: { tunnel: "t2", hostname: "blog.example.com", service: "http://blog:2368" } }],
  ])
})

test("the origin's TLS check is skipped only when asked", async () => {
  const { calls } = hpRender()
  const user = userEvent.setup()
  await hpTable()
  const form = hpForm()
  await user.click(within(form).getByRole("radio", { name: "darkpanel-c1" }))
  await user.type(within(form).getByLabelText("Hostname"), "panel.example.com")
  await user.type(within(form).getByLabelText("Service"), "https://172.31.255.2:8443")
  await user.click(within(form).getByRole("checkbox", { name: "Do not verify the origin's TLS certificate" }))
  await user.click(within(form).getByRole("button", { name: "Add hostname" }))
  await screen.findByRole("dialog", { name: "Add panel.example.com" })
  expect(hpPlanCalls(calls)[0]?.body).toEqual({ input: { tunnel: "t1", hostname: "panel.example.com", service: "https://172.31.255.2:8443", noTLSVerify: true } })
})

test("a form that is not complete says so and plans nothing", async () => {
  const { calls } = hpRender()
  const user = userEvent.setup()
  await hpTable()
  const form = hpForm()
  await user.type(within(form).getByLabelText("Hostname"), "blog.example.com")
  await user.click(within(form).getByRole("button", { name: "Add hostname" }))
  expect((await within(form).findByRole("alert")).textContent).toBe("Choose a tunnel and enter the hostname and the service.")
  expect(hpPlanCalls(calls)).toHaveLength(0)
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("the only tunnel is chosen without a click", async () => {
  const { calls } = hpRender({ [TUNNELS]: { body: { output: { tunnels: [cfTunnel({ id: "t1", name: "darkpanel-c1" })] } } } })
  const user = userEvent.setup()
  await hpTable()
  const form = hpForm()
  expect(within(form).getByRole("radio", { name: "darkpanel-c1" }).getAttribute("aria-checked")).toBe("true")
  await user.type(within(form).getByLabelText("Hostname"), "blog.example.com")
  await user.type(within(form).getByLabelText("Service"), "http://blog:2368")
  await user.click(within(form).getByRole("button", { name: "Add hostname" }))
  await screen.findByRole("dialog", { name: "Add blog.example.com" })
  expect((hpPlanCalls(calls)[0]?.body as { input: { tunnel: string } }).input.tunnel).toBe("t1")
})

test("without a tunnel the form is replaced by a pointer to the tunnels page", async () => {
  hpRender({ [TUNNELS]: { body: { output: { tunnels: [] } } }, [HOSTNAMES]: { body: { output: { hostnames: [] } } } })
  expect((await screen.findByText("No hostnames yet")).textContent).toBe("No hostnames yet")
  expect(screen.queryByRole("form", { name: "Add hostname" })).toBeNull()
  expect(screen.getByRole("link", { name: "Create a tunnel first" }).getAttribute("href")).toBe("/cloudflare/tunnels")
})

test("an account that is not connected shows the reason", async () => {
  const problem = { status: 409, body: { type: "about:blank", title: "Not connected", status: 409, detail: "no Cloudflare account is connected", code: "cloud.not_connected" } }
  hpRender({ [TUNNELS]: problem, [HOSTNAMES]: problem })
  expect((await screen.findByRole("alert")).textContent).toBe("no Cloudflare account is connected")
  expect(screen.queryByRole("form", { name: "Add hostname" })).toBeNull()
})

test("has no accessibility violations", async () => {
  const { container } = hpRender()
  await hpTable()
  expect(await axe(container)).toHaveNoViolations()
})
