import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { PrefsForm } from "./prefs-form"
import type { PrefsFields, TailscalePeer, TailscaleStatus } from "./types"

const PF_PLAN = "POST /api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan"

function pfPeer(over: Partial<TailscalePeer> = {}): TailscalePeer {
  return {
    id: "n9",
    hostName: "gw",
    dnsName: "gw.tail1234.ts.net",
    os: "linux",
    addresses: ["100.64.0.9"],
    tags: [],
    online: true,
    connection: "direct",
    endpoint: "203.0.113.9:41641",
    region: "",
    exitNode: false,
    exitNodeOption: true,
    lastHandshake: null,
    ...over,
  }
}

function pfStatus(over: Partial<TailscaleStatus> = {}): TailscaleStatus {
  return {
    version: "1.104.1",
    backendState: "Running",
    self: { id: "n1", hostName: "web-1", dnsName: "web-1.tail1234.ts.net", os: "linux", addresses: ["100.64.0.1"], tags: [] },
    tailnet: "example.com",
    magicDNSSuffix: "tail1234.ts.net",
    preferredDerp: "fra",
    runSSH: true,
    peers: [pfPeer()],
    health: [],
    ...over,
  }
}

const pfPrefs: PrefsFields = {
  wantRunning: true,
  advertiseRoutes: ["10.0.0.0/24"],
  exitNode: "",
  advertiseExitNode: false,
  acceptRoutes: true,
  shieldsUp: false,
  runSSH: true,
}

function pfRender(status: TailscaleStatus, prefs: PrefsFields) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": { body: { module: "tailscale", available: true, reason: null, actions: [] } },
    [PF_PLAN]: {
      body: {
        planId: "p1", hostId: "h1", action: "tailscale.prefs.set", version: 1, inputHash: "h",
        diff: [{ op: "~", text: "prefs" }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null,
      },
    },
  })
  const { container } = renderWithProviders(<PrefsForm hostId="h1" status={status} prefs={prefs} />)
  return { calls, container }
}

async function pfPlanned(calls: FetchCall[]): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: "Change the Tailscale preferences" })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

const pfApply = () => screen.getByRole("button", { name: "Apply preferences" })

test("shows the node's preferences and offers nothing to apply until one changes", () => {
  pfRender(pfStatus(), pfPrefs)
  expect(screen.getByRole("checkbox", { name: "Keep Tailscale running", checked: true })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Accept subnet routes", checked: true })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Shields up", checked: false })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Tailscale SSH", checked: true })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Advertise as an exit node", checked: false })).toBeTruthy()
  expect((screen.getByLabelText("Exit node") as HTMLInputElement).value).toBe("")
  expect((screen.getByLabelText("Advertised routes (comma-separated)") as HTMLInputElement).value).toBe("10.0.0.0/24")
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("a preference the node did not report is taken from the status", () => {
  pfRender(pfStatus({ backendState: "Stopped", runSSH: true, peers: [pfPeer({ id: "n9", exitNode: true })] }), {})
  expect(screen.getByRole("checkbox", { name: "Keep Tailscale running", checked: false })).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Tailscale SSH", checked: true })).toBeTruthy()
  expect((screen.getByLabelText("Exit node") as HTMLInputElement).value).toBe("n9")
})

test("plans only the fields that changed", async () => {
  const { calls } = pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Accept subnet routes" }))
  await user.click(screen.getByRole("checkbox", { name: "Shields up" }))
  await user.click(pfApply())
  expect(await pfPlanned(calls)).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.prefs.set/v1/plan",
    body: { input: { acceptRoutes: false, shieldsUp: true } },
  })
})

test("sends the advertised routes and the exit node as typed", async () => {
  const { calls } = pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  const routes = screen.getByLabelText("Advertised routes (comma-separated)")
  await user.clear(routes)
  await user.type(routes, "10.0.0.0/24, 192.168.1.0/24")
  await user.type(screen.getByLabelText("Exit node"), "100.64.0.9")
  await user.click(pfApply())
  expect((await pfPlanned(calls)).body).toEqual({ input: { advertiseRoutes: ["10.0.0.0/24", "192.168.1.0/24"], exitNode: "100.64.0.9" } })
})

test("clearing the exit node and the routes sends an empty value for each", async () => {
  const { calls } = pfRender(pfStatus(), { ...pfPrefs, exitNode: "n9" })
  const user = userEvent.setup()
  await user.clear(screen.getByLabelText("Exit node"))
  await user.clear(screen.getByLabelText("Advertised routes (comma-separated)"))
  await user.click(pfApply())
  expect((await pfPlanned(calls)).body).toEqual({ input: { exitNode: "", advertiseRoutes: [] } })
})

test("a default route cannot be advertised as a route", async () => {
  pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  const routes = screen.getByLabelText("Advertised routes (comma-separated)")
  await user.clear(routes)
  await user.type(routes, "0.0.0.0/0")
  expect(screen.getByText("Use the exit node option to advertise a default route.")).toBeTruthy()
  expect(routes.getAttribute("aria-invalid")).toBe("true")
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("a logged-out node cannot have its preferences changed", () => {
  pfRender(pfStatus({ backendState: "NeedsLogin", self: null }), pfPrefs)
  expect(screen.getByText("Log the node in to change its preferences.")).toBeTruthy()
  expect(screen.getByRole("checkbox", { name: "Shields up" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByLabelText("Exit node").hasAttribute("disabled")).toBe(true)
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("closing the change dialog shows the node's values again", async () => {
  const { calls } = pfRender(pfStatus(), pfPrefs)
  const user = userEvent.setup()
  await user.click(screen.getByRole("checkbox", { name: "Accept subnet routes" }))
  await user.click(pfApply())
  await pfPlanned(calls)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(screen.getByRole("checkbox", { name: "Accept subnet routes", checked: true })).toBeTruthy()
  expect(pfApply().hasAttribute("disabled")).toBe(true)
})

test("the preferences form has no accessibility violations", async () => {
  const { container } = pfRender(pfStatus(), pfPrefs)
  expect(await axe(container)).toHaveNoViolations()
})
