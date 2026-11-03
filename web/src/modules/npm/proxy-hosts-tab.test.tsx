import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { npProxyHost } from "./fixtures"
import { ProxyHostsTab } from "./proxy-hosts-tab"
import type { ProxyHostView } from "./types"

const PH_HOST = "/api/v1/hosts/h1"
const PH_LIST = `POST ${PH_HOST}/actions/npm.proxyhost.list/v1/read`
const PH_NAMES = "app.example.com, www.example.com"

function phPlan(call: FetchCall) {
  const parts = call.path.split("/")
  return {
    body: {
      planId: "p1", hostId: "h1", action: parts[parts.length - 3] ?? "", version: 1, inputHash: "h",
      diff: [{ op: "~", text: "change" }], guards: [], expiresAt: "2026-11-02T13:00:00Z", confirmation: null,
    },
  }
}

function phHosts(): ProxyHostView[] {
  return [
    npProxyHost({ id: 3, domains: ["app.example.com", "www.example.com"], forwardScheme: "http", forwardHost: "web", forwardPort: 8080, certificateId: 5, accessListId: 2, sslForced: true, hsts: true, websockets: false, reserved: "", unclaimed: false }),
    npProxyHost({ id: 4, domains: ["npm.example.com"], forwardScheme: "http", forwardHost: "127.0.0.1", forwardPort: 81, certificateId: 6, accessListId: 0, sslForced: true, hsts: true, websockets: true, reserved: "npm.admin.expose", unclaimed: false }),
    npProxyHost({ id: 9, domains: ["old.example.com"], forwardScheme: "https", forwardHost: "10.0.0.5", forwardPort: 443, certificateId: 0, accessListId: 0, sslForced: false, hsts: false, websockets: false, reserved: "", unclaimed: true }),
  ]
}

function phRender(items: ProxyHostView[] = phHosts(), routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [PH_LIST]: { body: { output: { items } } },
    [`GET ${PH_HOST}/modules/npm/availability`]: { body: { module: "npm", available: true, reason: null, actions: [] } },
    [`POST ${PH_HOST}/actions/npm.proxyhost.add/v1/plan`]: phPlan,
    [`POST ${PH_HOST}/actions/npm.proxyhost.update/v1/plan`]: phPlan,
    [`POST ${PH_HOST}/actions/npm.proxyhost.remove/v1/plan`]: phPlan,
    ...routes,
  })
  const result = renderWithProviders(<ProxyHostsTab hostId="h1" />)
  return { calls, ...result }
}

function phCells(n: number): (string | null)[] {
  const row = within(screen.getByRole("table", { name: "Proxy hosts" })).getAllByRole("row")[n]
  if (row === undefined) throw new Error(`no row ${n}`)
  return within(row).getAllByRole("cell").map((c) => c.textContent)
}

function phPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

function phPlanPath(calls: FetchCall[]): string | undefined {
  return calls.find((c) => c.method === "POST" && c.path.endsWith("/plan"))?.path
}

test("lists each proxy host with its target, certificate, options and marks", async () => {
  phRender()
  await screen.findByRole("table", { name: "Proxy hosts" })
  expect(phCells(1).slice(0, 7)).toEqual(["3", PH_NAMES, "http://web:8080", "5", "2", "force SSL, HSTS", ""])
  expect(phCells(2).slice(0, 7)).toEqual(["4", "npm.example.com", "http://127.0.0.1:81", "6", "none", "force SSL, HSTS, websockets", "Reserved: npm.admin.expose"])
  expect(phCells(3).slice(0, 7)).toEqual(["9", "old.example.com", "https://10.0.0.5:443", "none", "none", "", "Unclaimed Darkpanel object"])
})

test("says so when there are no proxy hosts", async () => {
  phRender([])
  expect((await screen.findByText("No proxy hosts")).textContent).toBe("No proxy hosts")
  expect(screen.queryByRole("table")).toBeNull()
})

test("shows the reason when the proxy hosts cannot be read", async () => {
  phRender([], {
    [PH_LIST]: { status: 409, body: { type: "about:blank", title: "Unhealthy", status: 409, detail: "Nginx Proxy Manager is not running", code: "npm.unhealthy" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("Nginx Proxy Manager is not running")
})

test("a reserved proxy host cannot be edited or removed here", async () => {
  phRender()
  await screen.findByRole("table", { name: "Proxy hosts" })
  expect(screen.getByRole("button", { name: "Edit npm.example.com" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Remove npm.example.com" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Edit old.example.com" }).hasAttribute("disabled")).toBe(false)
  expect(screen.getByRole("button", { name: `Remove ${PH_NAMES}` }).hasAttribute("disabled")).toBe(false)
})

test("removing a proxy host plans npm.proxyhost.remove with its id", async () => {
  const { calls } = phRender()
  await screen.findByRole("table", { name: "Proxy hosts" })
  await userEvent.setup().click(screen.getByRole("button", { name: "Remove old.example.com" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove proxy host old.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(phPlanPath(calls)).toBe(`${PH_HOST}/actions/npm.proxyhost.remove/v1/plan`)
  expect(phPlanBodies(calls)).toEqual([{ input: { id: 9 } }])
})

test("the add form needs domains, a host and a valid port, and plans npm.proxyhost.add with every field", async () => {
  const { calls } = phRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Add proxy host" }))
  const form = await screen.findByRole("form", { name: "Proxy host" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Domains"), "App.Example.com, www.example.com")
  await user.type(within(form).getByLabelText("Forward host"), " web ")
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Forward port"), "8080")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.click(within(form).getByRole("radio", { name: "https" }))
  await user.type(within(form).getByLabelText("Certificate ID (optional)"), "5")
  await user.click(within(form).getByRole("checkbox", { name: "Force SSL" }))
  await user.click(within(form).getByRole("checkbox", { name: "Websockets" }))
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: `Add proxy host ${PH_NAMES}` })
  await within(dialog).findByLabelText("Planned changes")
  expect(phPlanPath(calls)).toBe(`${PH_HOST}/actions/npm.proxyhost.add/v1/plan`)
  expect(phPlanBodies(calls)).toEqual([
    {
      input: {
        domains: ["app.example.com", "www.example.com"],
        forwardScheme: "https",
        forwardHost: "web",
        forwardPort: 8080,
        certificateId: 5,
        accessListId: 0,
        sslForced: true,
        hsts: false,
        websockets: true,
      },
    },
  ])
})

test.each([
  ["Forward port", "0"],
  ["Forward port", "70000"],
  ["Certificate ID (optional)", "x"],
  ["Access list ID (optional)", "-2"],
])("%s %j keeps Plan off", async (label, typed) => {
  phRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Add proxy host" }))
  const form = await screen.findByRole("form", { name: "Proxy host" })
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.type(within(form).getByLabelText("Forward host"), "web")
  await user.type(within(form).getByLabelText("Forward port"), "8080")
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(false)
  const field = within(form).getByLabelText(label)
  await user.clear(field)
  await user.type(field, typed)
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})

test("editing prefills the form and plans npm.proxyhost.update with the id and every field", async () => {
  const { calls } = phRender()
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Proxy hosts" })
  await user.click(screen.getByRole("button", { name: `Edit ${PH_NAMES}` }))
  const form = await screen.findByRole("form", { name: "Proxy host" })
  const value = (label: string) => (within(form).getByLabelText(label) as HTMLInputElement).value
  expect(value("Domains")).toBe(PH_NAMES)
  expect(value("Forward host")).toBe("web")
  expect(value("Forward port")).toBe("8080")
  expect(value("Certificate ID (optional)")).toBe("5")
  expect(value("Access list ID (optional)")).toBe("2")
  expect(within(form).getByRole("radio", { name: "http", checked: true })).toBeTruthy()
  expect(within(form).getByRole("checkbox", { name: "Force SSL", checked: true })).toBeTruthy()
  expect(within(form).getByRole("checkbox", { name: "HSTS", checked: true })).toBeTruthy()
  expect(within(form).getByRole("checkbox", { name: "Websockets", checked: false })).toBeTruthy()
  const port = within(form).getByLabelText("Forward port")
  await user.clear(port)
  await user.type(port, "9090")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: `Update proxy host ${PH_NAMES}` })
  await within(dialog).findByLabelText("Planned changes")
  expect(phPlanPath(calls)).toBe(`${PH_HOST}/actions/npm.proxyhost.update/v1/plan`)
  expect(phPlanBodies(calls)).toEqual([
    {
      input: {
        id: 3,
        domains: ["app.example.com", "www.example.com"],
        forwardScheme: "http",
        forwardHost: "web",
        forwardPort: 9090,
        certificateId: 5,
        accessListId: 2,
        sslForced: true,
        hsts: true,
        websockets: false,
      },
    },
  ])
})

test("the add form starts empty each time it opens", async () => {
  phRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Add proxy host" }))
  await user.type(within(await screen.findByRole("form", { name: "Proxy host" })).getByLabelText("Domains"), "app.example.com")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Add proxy host" }))
  expect((within(await screen.findByRole("form", { name: "Proxy host" })).getByLabelText("Domains") as HTMLInputElement).value).toBe("")
})

test("the table has no accessibility violations", async () => {
  phRender()
  const table = await screen.findByRole("table", { name: "Proxy hosts" })
  expect(await axe(table)).toHaveNoViolations()
})
