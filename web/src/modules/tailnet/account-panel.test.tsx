import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AccountPanel } from "./account-panel"
import { tnAccount } from "./fixtures"
import type { TailnetAccountStatus } from "./types"

vi.mock("@/components/capability-list", () => ({
  CapabilityList: ({ capabilities }: { capabilities: { name: string; state: string }[] }) => (
    <ul aria-label="Capabilities">
      {capabilities.map((c) => (
        <li key={c.name}>{`${c.name}: ${c.state}`}</li>
      ))}
    </ul>
  ),
}))

const AP_GET = "POST /api/v1/actions/tailnet.account.get/v1/read"
const AP_CONNECTED_AT = "2026-10-29T08:15:00Z"

const apDisconnected: TailnetAccountStatus = { connected: false }
const apConnected: TailnetAccountStatus = {
  connected: true,
  account: tnAccount({
    tailnet: "example.com",
    clientId: "k123CNTRL",
    tagsAllowed: ["tag:server", "tag:darkpanel"],
    capabilities: [
      { name: "devices:core", state: "ok" },
      { name: "auth_keys", state: "unverified" },
    ],
    connectedAt: AP_CONNECTED_AT,
  }),
}

function apPlan(call: FetchCall) {
  const action = call.path.split("/")[4] ?? ""
  return {
    body: {
      planId: "p1", hostId: null, action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z",
      confirmation: action === "tailnet.account.disconnect" ? "example.com" : null,
    },
  }
}

function apRender(status: TailnetAccountStatus, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [AP_GET]: { body: { output: status } },
    "POST /api/v1/actions/tailnet.account.connect/v1/plan": apPlan,
    "POST /api/v1/actions/tailnet.account.disconnect/v1/plan": apPlan,
    ...routes,
  })
  const result = renderWithProviders(<AccountPanel />)
  return { calls, ...result }
}

function apValue(label: string): string | null | undefined {
  return within(screen.getByLabelText("Tailnet account")).getByText(label, { selector: "dt" }).nextElementSibling?.textContent
}

function apPlanned(calls: FetchCall[]): { path: string; body: unknown }[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => ({ path: c.path, body: c.body }))
}

async function apFill(user: ReturnType<typeof userEvent.setup>, form: HTMLElement, values: { tailnet?: string; clientId?: string; secret?: string; tags?: string }) {
  if (values.tailnet !== undefined) await user.type(within(form).getByLabelText("Tailnet"), values.tailnet)
  if (values.clientId !== undefined) await user.type(within(form).getByLabelText("OAuth client ID"), values.clientId)
  if (values.secret !== undefined) await user.type(within(form).getByLabelText("OAuth client secret"), values.secret)
  if (values.tags !== undefined) await user.type(within(form).getByLabelText("Allowed tags"), values.tags)
}

test("shows the reason when the account cannot be read", async () => {
  apRender(apDisconnected, { [AP_GET]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the controller database is busy", code: "internal" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("the controller database is busy")
})

test("without an account it offers only Connect", async () => {
  apRender(apDisconnected)
  expect((await screen.findByText("No tailnet is connected.")).textContent).toBe("No tailnet is connected.")
  expect(screen.getByRole("button", { name: "Connect" })).toBeTruthy()
  expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull()
})

test("a connected account shows its tailnet, client, tags and capabilities", async () => {
  apRender(apConnected)
  await screen.findByLabelText("Tailnet account")
  expect(apValue("Tailnet")).toBe("example.com")
  expect(apValue("OAuth client")).toBe("k123CNTRL")
  expect(apValue("Allowed tags")).toBe("tag:server, tag:darkpanel")
  expect(apValue("Connected")).toBe(formatTime(AP_CONNECTED_AT))
  const capabilities = within(screen.getByRole("list", { name: "Capabilities" })).getAllByRole("listitem")
  expect(capabilities.map((li) => li.textContent)).toEqual(["devices:core: ok", "auth_keys: unverified"])
  expect(screen.queryByRole("button", { name: "Connect" })).toBeNull()
  expect(screen.queryByText("No tailnet is connected.")).toBeNull()
})

test("the connect form needs every field and well-formed tags, and hides the secret", async () => {
  apRender(apDisconnected)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Connect" }))
  const form = await screen.findByRole("form", { name: "Tailnet connection" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(within(form).getByLabelText("OAuth client secret").getAttribute("type")).toBe("password")
  expect(plan.hasAttribute("disabled")).toBe(true)
  await apFill(user, form, { tailnet: "example.com", clientId: "k123CNTRL", secret: "tskey-client-s3cret", tags: "server" })
  expect(within(form).getByText("Each tag must look like tag:name.").textContent).toBe("Each tag must look like tag:name.")
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.clear(within(form).getByLabelText("Allowed tags"))
  await user.type(within(form).getByLabelText("Allowed tags"), "tag:server")
  expect(within(form).queryByText("Each tag must look like tag:name.")).toBeNull()
  expect(plan.hasAttribute("disabled")).toBe(false)
})

test("planning the connection sends the trimmed fields and every tag", async () => {
  const { calls } = apRender(apDisconnected)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Connect" }))
  const form = await screen.findByRole("form", { name: "Tailnet connection" })
  await apFill(user, form, { tailnet: " example.com ", clientId: "k123CNTRL", secret: "tskey-client-s3cret", tags: "tag:server, tag:darkpanel  tag:ci" })
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Connect tailnet example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(apPlanned(calls)).toEqual([
    {
      path: "/api/v1/actions/tailnet.account.connect/v1/plan",
      body: { input: { tailnet: "example.com", clientId: "k123CNTRL", clientSecret: "tskey-client-s3cret", tagsAllowed: ["tag:server", "tag:darkpanel", "tag:ci"] } },
    },
  ])
})

test("the connect form forgets what was typed, the secret included, once the change closes", async () => {
  apRender(apDisconnected)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Connect" }))
  await apFill(user, await screen.findByRole("form", { name: "Tailnet connection" }), { tailnet: "example.com", clientId: "k123CNTRL", secret: "tskey-client-s3cret", tags: "tag:server" })
  await user.click(screen.getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog", { name: "Connect tailnet example.com" })).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(await screen.findByRole("button", { name: "Connect" }))
  const form = await screen.findByRole("form", { name: "Tailnet connection" })
  for (const label of ["Tailnet", "OAuth client ID", "OAuth client secret", "Allowed tags"]) {
    expect((within(form).getByLabelText(label) as HTMLInputElement).value).toBe("")
  }
})

test("dismissing the connect dialog without planning forgets what was typed", async () => {
  apRender(apDisconnected)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Connect" }))
  await apFill(user, await screen.findByRole("form", { name: "Tailnet connection" }), { tailnet: "example.com", clientId: "k123CNTRL", secret: "tskey-client-s3cret", tags: "tag:server" })
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Connect" }))
  const form = await screen.findByRole("form", { name: "Tailnet connection" })
  for (const label of ["Tailnet", "OAuth client ID", "OAuth client secret", "Allowed tags"]) {
    expect((within(form).getByLabelText(label) as HTMLInputElement).value).toBe("")
  }
})

test("an applied connection shows the account once the dialog closes", async () => {
  let reads = 0
  const { calls } = apRender(apDisconnected, {
    [AP_GET]: () => ({ body: { output: ++reads === 1 ? apDisconnected : apConnected } }),
    "POST /api/v1/actions/tailnet.account.connect/v1/apply": {
      body: { id: "c1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null },
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Connect" }))
  await apFill(user, await screen.findByRole("form", { name: "Tailnet connection" }), { tailnet: "example.com", clientId: "k123CNTRL", secret: "tskey-client-s3cret", tags: "tag:server" })
  await user.click(screen.getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Connect tailnet example.com" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
  await user.keyboard("{Escape}")
  expect(await screen.findByLabelText("Tailnet account")).toBeTruthy()
  expect(apValue("Tailnet")).toBe("example.com")
  expect(calls.filter((c) => c.path.endsWith("/tailnet.account.get/v1/read"))).toHaveLength(2)
})

test("Disconnect plans tailnet.account.disconnect, and abandoning cleanup is a separate choice", async () => {
  const { calls } = apRender(apConnected)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Disconnect" }))
  const first = await screen.findByRole("dialog", { name: "Disconnect example.com" })
  await within(first).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("checkbox", { name: "Abandon pending cleanup" }))
  await user.click(screen.getByRole("button", { name: "Disconnect" }))
  await within(await screen.findByRole("dialog", { name: "Disconnect example.com" })).findByLabelText("Planned changes")
  expect(apPlanned(calls)).toEqual([
    { path: "/api/v1/actions/tailnet.account.disconnect/v1/plan", body: { input: {} } },
    { path: "/api/v1/actions/tailnet.account.disconnect/v1/plan", body: { input: { abandonCleanup: true } } },
  ])
})

test("both states and the connect form have no accessibility violations", async () => {
  const first = apRender(apDisconnected)
  await screen.findByText("No tailnet is connected.")
  expect(await axe(first.container)).toHaveNoViolations()
  await userEvent.setup().click(screen.getByRole("button", { name: "Connect" }))
  expect(await axe(await screen.findByRole("dialog", { name: "Connect a tailnet" }))).toHaveNoViolations()
  first.unmount()
  const second = apRender(apConnected)
  await screen.findByLabelText("Tailnet account")
  expect(await axe(second.container)).toHaveNoViolations()
})
