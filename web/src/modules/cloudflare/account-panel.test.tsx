import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { formatTime } from "@/lib/format"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { AccountPanel } from "./account-panel"
import type { CloudflareAccountStatus, CloudflareAccountView } from "./types"

const AP_READ = "POST /api/v1/actions/cloudflare.account.get/v1/read"
const AP_CONNECT_PLAN = "POST /api/v1/actions/cloudflare.account.connect/v1/plan"
const AP_CONNECT_APPLY = "POST /api/v1/actions/cloudflare.account.connect/v1/apply"
const AP_DISCONNECT_PLAN = "POST /api/v1/actions/cloudflare.account.disconnect/v1/plan"
const AP_DISCONNECT_APPLY = "POST /api/v1/actions/cloudflare.account.disconnect/v1/apply"

const AP_ACCOUNT_ID = "0123456789abcdef0123456789abcdef"

const apView: CloudflareAccountView = {
  accountId: AP_ACCOUNT_ID,
  capabilities: [
    { name: "list tunnels", state: "ok" },
    { name: "list zones", state: "missing", detail: "cloud.unavailable" },
    { name: "tunnel write", state: "unverified" },
  ],
  connectedAt: "2026-10-30T08:00:00Z",
}

const apConnected: StubResponse = { body: { output: { connected: true, account: apView } satisfies CloudflareAccountStatus } }
const apDisconnected: StubResponse = { body: { output: { connected: false } satisfies CloudflareAccountStatus } }

function apPlan(action: string, over: Partial<Plan> = {}): Plan {
  return { planId: "p1", hostId: null, action, version: 1, inputHash: "h", diff: [{ op: "+", text: "connect the account" }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null, ...over }
}

const apApplied = { id: "c1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

// apReads answers the account read with each response in turn, then the last one again.
function apReads(...responses: StubResponse[]): (call: FetchCall) => StubResponse {
  let n = 0
  return () => responses[Math.min(n++, responses.length - 1)] ?? apDisconnected
}

function apBodies(calls: FetchCall[], suffix: string): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith(suffix)).map((c) => c.body)
}

function apRead(calls: FetchCall[]): number {
  return calls.filter((c) => c.path.endsWith("cloudflare.account.get/v1/read")).length
}

test("a connected account shows its id, when it connected and the capabilities of its token", async () => {
  stubFetch({ [AP_READ]: apConnected })
  renderWithProviders(<AccountPanel />)
  const facts = await screen.findByLabelText("Cloudflare account")
  expect(within(facts).getByText("Account ID", { selector: "dt" }).nextElementSibling?.textContent).toBe(AP_ACCOUNT_ID)
  expect(within(facts).getByText("Connected", { selector: "dt" }).nextElementSibling?.textContent).toBe(formatTime(apView.connectedAt))
  const capabilities = screen.getByRole("group", { name: "Token capabilities" })
  expect(within(capabilities).getByText(/list tunnels/)).toBeTruthy()
  expect(within(capabilities).getByText(/tunnel write/)).toBeTruthy()
  expect(within(capabilities).getByText(/could not check/i)).toBeTruthy()
  expect(screen.queryByLabelText("API token")).toBeNull()
})

test("a failed read shows its message", async () => {
  stubFetch({ [AP_READ]: { status: 500, body: { type: "about:blank", title: "Internal error", status: 500, detail: "the controller database is locked", code: "internal" } } })
  renderWithProviders(<AccountPanel />)
  expect((await screen.findByRole("alert")).textContent).toBe("the controller database is locked")
})

test("without an account the form states the permissions the token needs", async () => {
  stubFetch({ [AP_READ]: apDisconnected })
  renderWithProviders(<AccountPanel />)
  await screen.findByLabelText("Account ID")
  const api = screen.getByLabelText("API token")
  expect(api.getAttribute("type")).toBe("password")
  const items = within(screen.getByRole("list", { name: "Required token permissions" })).getAllByRole("listitem").map((li) => li.textContent ?? "")
  expect(items.length).toBeGreaterThanOrEqual(3)
  for (const permission of ["Cloudflare Tunnel Write", "Zone Read", "DNS Write"]) {
    expect(items.filter((text) => text.includes(permission))).toHaveLength(1)
  }
  expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull()
})

test("connecting plans the typed account and token, applies, and shows the account once it is read again", async () => {
  const calls = stubFetch({
    [AP_READ]: apReads(apDisconnected, apConnected),
    [AP_CONNECT_PLAN]: { body: apPlan("cloudflare.account.connect") },
    [AP_CONNECT_APPLY]: { body: apApplied },
  })
  renderWithProviders(<AccountPanel />)
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Account ID"), `  ${AP_ACCOUNT_ID} `)
  await user.type(screen.getByLabelText("API token"), "example-token")
  await user.click(screen.getByRole("button", { name: "Connect" }))
  const dialog = await screen.findByRole("dialog", { name: "Connect Cloudflare" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ connect the account")
  expect(apBodies(calls, "cloudflare.account.connect/v1/plan")).toEqual([{ input: { accountId: AP_ACCOUNT_ID, apiToken: "example-token" } }])
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  await screen.findByLabelText("Cloudflare account")
  expect(apRead(calls)).toBe(2)
  expect(apBodies(calls, "cloudflare.account.connect/v1/apply")).toEqual([{ planId: "p1" }])
  expect(screen.queryByDisplayValue("example-token")).toBeNull()
})

test("closing the connect dialog without applying forgets the token and keeps the account id", async () => {
  stubFetch({ [AP_READ]: apDisconnected, [AP_CONNECT_PLAN]: { body: apPlan("cloudflare.account.connect") } })
  renderWithProviders(<AccountPanel />)
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Account ID"), AP_ACCOUNT_ID)
  await user.type(screen.getByLabelText("API token"), "example-token")
  await user.click(screen.getByRole("button", { name: "Connect" }))
  const dialog = await screen.findByRole("dialog", { name: "Connect Cloudflare" })
  await within(dialog).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect((screen.getByLabelText("API token") as HTMLInputElement).value).toBe("")
  expect((screen.getByLabelText("Account ID") as HTMLInputElement).value).toBe(AP_ACCOUNT_ID)
})

test("the form plans nothing until both fields are filled", async () => {
  const calls = stubFetch({ [AP_READ]: apDisconnected, [AP_CONNECT_PLAN]: { body: apPlan("cloudflare.account.connect") } })
  renderWithProviders(<AccountPanel />)
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Account ID"), AP_ACCOUNT_ID)
  await user.click(screen.getByRole("button", { name: "Connect" }))
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(apBodies(calls, "/plan")).toEqual([])
})

test("disconnecting asks for the account id and plans with no input", async () => {
  const calls = stubFetch({
    [AP_READ]: apReads(apConnected, apDisconnected),
    [AP_DISCONNECT_PLAN]: { body: apPlan("cloudflare.account.disconnect", { planId: "p2", diff: [{ op: "-", text: `account ${AP_ACCOUNT_ID}` }], confirmation: AP_ACCOUNT_ID }) },
    [AP_DISCONNECT_APPLY]: { body: { ...apApplied, id: "c2", class: "irreversible" } },
  })
  renderWithProviders(<AccountPanel />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Disconnect" }))
  const dialog = await screen.findByRole("dialog", { name: "Disconnect Cloudflare" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe(`- account ${AP_ACCOUNT_ID}`)
  expect(apBodies(calls, "cloudflare.account.disconnect/v1/plan")).toEqual([{ input: {} }])
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  expect(apply.hasAttribute("disabled")).toBe(true)
  await user.type(within(dialog).getByLabelText(`Type ${AP_ACCOUNT_ID} to confirm`), AP_ACCOUNT_ID)
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  expect(apBodies(calls, "cloudflare.account.disconnect/v1/apply")).toEqual([{ planId: "p2", confirmation: AP_ACCOUNT_ID }])
  await screen.findByLabelText("API token")
})

test("abandoning the pending cleanup is sent as abandonCleanup", async () => {
  const calls = stubFetch({ [AP_READ]: apConnected, [AP_DISCONNECT_PLAN]: { body: apPlan("cloudflare.account.disconnect", { confirmation: AP_ACCOUNT_ID }) } })
  renderWithProviders(<AccountPanel />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("checkbox", { name: "Abandon pending cleanup" }))
  await user.click(screen.getByRole("button", { name: "Disconnect" }))
  await screen.findByRole("dialog", { name: "Disconnect Cloudflare" })
  await waitFor(() => expect(apBodies(calls, "cloudflare.account.disconnect/v1/plan")).toEqual([{ input: { abandonCleanup: true } }]))
})

test("a refused disconnect shows the refusal in the dialog", async () => {
  stubFetch({
    [AP_READ]: apConnected,
    [AP_DISCONNECT_PLAN]: { status: 409, body: { type: "about:blank", title: "Cleanup pending on the account", status: 409, detail: "hostname app.example.com is not active", code: "cloud.cleanup_pending" } },
  })
  renderWithProviders(<AccountPanel />)
  await userEvent.setup().click(await screen.findByRole("button", { name: "Disconnect" }))
  const dialog = await screen.findByRole("dialog", { name: "Disconnect Cloudflare" })
  expect((await within(dialog).findByRole("alert")).textContent).toBe("hostname app.example.com is not active")
})

test("both states have no accessibility violations", async () => {
  stubFetch({ [AP_READ]: apDisconnected })
  const first = renderWithProviders(<AccountPanel />)
  await screen.findByLabelText("Account ID")
  expect(await axe(first.container)).toHaveNoViolations()
  first.unmount()
  stubFetch({ [AP_READ]: apConnected })
  const second = renderWithProviders(<AccountPanel />)
  await screen.findByLabelText("Cloudflare account")
  expect(await axe(second.container)).toHaveNoViolations()
})
