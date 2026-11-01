import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { NodeControls } from "./node-controls"
import type { TailscaleStatus } from "./types"

const ncLoggedIn: TailscaleStatus = {
  version: "1.104.1",
  backendState: "Running",
  self: { id: "n1", hostName: "web-1", dnsName: "web-1.tail1234.ts.net", os: "linux", addresses: ["100.64.0.1"], tags: [] },
  tailnet: "example.com",
  magicDNSSuffix: "tail1234.ts.net",
  preferredDerp: "fra",
  runSSH: false,
  peers: [],
  health: [],
}
const ncLoggedOut: TailscaleStatus = { ...ncLoggedIn, backendState: "NeedsLogin", self: null, tailnet: "", magicDNSSuffix: "", preferredDerp: "" }

function ncRender(status: TailscaleStatus) {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return {
      body: {
        planId: "p1", hostId: "h1", action, version: 1, inputHash: "h",
        diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null,
      },
    }
  }
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": { body: { module: "tailscale", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/tailscale.node.login/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/tailscale.node.logout/v1/plan": plan,
  })
  const { container } = renderWithProviders(<NodeControls hostId="h1" status={status} />)
  return { calls, container }
}

async function ncPlanned(calls: FetchCall[], title: string): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

test("a logged-out node offers a login with an auth key and no logout", () => {
  ncRender(ncLoggedOut)
  const form = screen.getByRole("form", { name: "Log in" })
  const key = within(form).getByLabelText("Auth key")
  expect(key.getAttribute("type")).toBe("password")
  expect(within(form).getByRole("button", { name: "Log in" }).hasAttribute("disabled")).toBe(true)
  expect(screen.queryByRole("button", { name: "Log out" })).toBeNull()
})

test("logs in with the key, hostname and tags, and forgets the key once the change is planned", async () => {
  const { calls } = ncRender(ncLoggedOut)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Log in" })
  await user.type(within(form).getByLabelText("Auth key"), "tskey-auth-example")
  await user.type(within(form).getByLabelText("Hostname (optional)"), "web-2")
  await user.type(within(form).getByLabelText("Tags (comma-separated)"), "tag:server, tag:ci")
  await user.click(within(form).getByRole("button", { name: "Log in" }))
  expect(await ncPlanned(calls, "Log the node in to the tailnet")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.node.login/v1/plan",
    body: { input: { authKey: "tskey-auth-example", hostname: "web-2", advertiseTags: ["tag:server", "tag:ci"] } },
  })
  expect((screen.getByLabelText("Auth key") as HTMLInputElement).value).toBe("")
})

test("a login with only a key sends only the key", async () => {
  const { calls } = ncRender(ncLoggedOut)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Log in" })
  await user.type(within(form).getByLabelText("Auth key"), "tskey-auth-example")
  await user.click(within(form).getByRole("button", { name: "Log in" }))
  expect((await ncPlanned(calls, "Log the node in to the tailnet")).body).toEqual({ input: { authKey: "tskey-auth-example" } })
})

test("a logged-in node offers Log out and a login again that asks for relogin", async () => {
  const { calls } = ncRender(ncLoggedIn)
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Log in" })
  await user.type(within(form).getByLabelText("Auth key"), "tskey-auth-example")
  await user.click(within(form).getByRole("button", { name: "Log in again" }))
  expect(await ncPlanned(calls, "Log the node in again")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.node.login/v1/plan",
    body: { input: { authKey: "tskey-auth-example", relogin: true } },
  })
})

test("Log out plans tailscale.node.logout with no input", async () => {
  const { calls } = ncRender(ncLoggedIn)
  await userEvent.setup().click(screen.getByRole("button", { name: "Log out" }))
  expect(await ncPlanned(calls, "Log the node out of the tailnet")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.node.logout/v1/plan",
    body: { input: {} },
  })
})

test("the node controls have no accessibility violations", async () => {
  const { container } = ncRender(ncLoggedIn)
  expect(await axe(container)).toHaveNoViolations()
})
