import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { tnKey } from "./fixtures"
import { KeysPage } from "./keys-page"
import type { TailnetAccountStatus, TailnetAccountView } from "./types"

const KY_KEYS = "POST /api/v1/actions/tailnet.authkey.list/v1/read"
const KY_ACCOUNT = "POST /api/v1/actions/tailnet.account.get/v1/read"
const KY_PLAN = "POST /api/v1/actions/tailnet.authkey.create/v1/plan"
const KY_APPLY = "POST /api/v1/actions/tailnet.authkey.create/v1/apply"
const KY_SECRET = "tskey-auth-example-secret"

const kyAccount: TailnetAccountView = {
  tailnet: "example.com",
  clientId: "client-1",
  tagsAllowed: ["tag:server", "tag:db"],
  capabilities: [],
  connectedAt: "2026-10-30T08:00:00Z",
}
const kyConnected: StubResponse = { body: { output: { connected: true, account: kyAccount } satisfies TailnetAccountStatus } }
const kyKeys: StubResponse = {
  body: {
    output: {
      keys: [
        tnKey({ description: "darkpanel-01JD0000000000000000000001", kind: "change", state: "live", keyId: "k-live" }),
        tnKey({ description: "darkpanel-enroll-h2", kind: "enroll", state: "retired", ownerId: "h2", keyId: "" }),
      ],
    },
  },
}

const kyPlan: StubResponse = {
  body: {
    planId: "p1", hostId: null, action: "tailnet.authkey.create", version: 1, inputHash: "h",
    diff: [{ op: "+", text: "auth key darkpanel-c3 with tag:server" }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null,
  },
}

function kyChange(over: Record<string, unknown> = {}): StubResponse {
  return {
    body: {
      id: "c3", hostId: null, action: "tailnet.authkey.create", version: 1, class: "compensable", state: "completed", actor: "user:u1",
      createdAt: "2026-10-30T08:00:00Z", updatedAt: "2026-10-30T08:00:00Z", revertDeadline: null,
      output: { id: "k1", key: KY_SECRET, expires: "2026-10-31T08:00:00Z", description: "darkpanel-c3" },
      error: null, compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
    },
  }
}

function kyPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("tailnet.authkey.create/v1/plan")).map((c) => c.body)
}

function kyRender(over: Record<string, StubResponse> = {}) {
  const calls = stubFetch({ [KY_KEYS]: kyKeys, [KY_ACCOUNT]: kyConnected, [KY_PLAN]: kyPlan, ...over })
  return { calls, ...renderWithProviders(<KeysPage />) }
}

test("lists the keys Darkpanel created with their state and never a secret", async () => {
  kyRender()
  const table = await screen.findByRole("table", { name: "Auth keys" })
  const live = within(within(table).getByText("darkpanel-01JD0000000000000000000001").closest("tr")!)
  expect(live.getByText("change")).toBeTruthy()
  expect(live.getByText("live")).toBeTruthy()
  expect(live.getByText("k-live")).toBeTruthy()
  const retired = within(within(table).getByText("darkpanel-enroll-h2").closest("tr")!)
  expect(retired.getByText("enroll")).toBeTruthy()
  expect(retired.getByText("retired")).toBeTruthy()
  expect(within(table).queryByText(/tskey-/)).toBeNull()
})

test("creating a key plans the chosen options, applies, and shows the key once without caching it", async () => {
  const { calls, client } = kyRender({ [KY_APPLY]: kyChange() })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("checkbox", { name: "tag:server" }))
  await user.click(screen.getByRole("checkbox", { name: "Reusable" }))
  await user.click(screen.getByRole("checkbox", { name: "Pre-authorized" }))
  await user.clear(screen.getByLabelText("Expiry (seconds)"))
  await user.type(screen.getByLabelText("Expiry (seconds)"), "3600")
  await user.click(screen.getByRole("button", { name: "Create key" }))
  const plan = await screen.findByRole("dialog", { name: "Create an auth key" })
  expect((await within(plan).findByLabelText("Planned changes")).textContent).toBe("+ auth key darkpanel-c3 with tag:server")
  expect(kyPlanBodies(calls)).toEqual([{ input: { tags: ["tag:server"], expirySeconds: 3600, reusable: true, preauthorized: true } }])
  const apply = within(plan).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)

  const done = await screen.findByRole("dialog", { name: "Auth key created" })
  expect(within(done).getByDisplayValue(KY_SECRET)).toBeTruthy()
  expect(within(done).getByText("Copy the key now. It will not be shown again.")).toBeTruthy()
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("tailnet.authkey.list/v1/read")).length).toBeGreaterThan(1))
  expect(JSON.stringify(client.getQueryCache().getAll().map((q) => q.state.data))).not.toContain(KY_SECRET)

  await user.click(within(done).getByRole("button", { name: "Done" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(screen.queryByDisplayValue(KY_SECRET)).toBeNull()
})

test("the defaults are a day and no flags, and every ticked tag is sent", async () => {
  const { calls } = kyRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("checkbox", { name: "tag:server" }))
  await user.click(screen.getByRole("checkbox", { name: "tag:db" }))
  await user.click(screen.getByRole("button", { name: "Create key" }))
  await screen.findByRole("dialog", { name: "Create an auth key" })
  expect(kyPlanBodies(calls)).toEqual([{ input: { tags: ["tag:server", "tag:db"], expirySeconds: 86400 } }])
})

const kyInvalid = [
  { name: "no tag is chosen", expiry: "86400", tag: false, message: "Choose at least one tag." },
  { name: "the expiry is zero", expiry: "0", tag: true, message: "Enter a whole number of seconds from 1 to 7776000." },
  { name: "the expiry is over 90 days", expiry: "7776001", tag: true, message: "Enter a whole number of seconds from 1 to 7776000." },
  { name: "the expiry is not a whole number", expiry: "1.5", tag: true, message: "Enter a whole number of seconds from 1 to 7776000." },
]

test.each(kyInvalid)("nothing is planned when $name", async ({ expiry, tag, message }) => {
  const { calls } = kyRender()
  const user = userEvent.setup()
  if (tag) await user.click(await screen.findByRole("checkbox", { name: "tag:server" }))
  await user.clear(await screen.findByLabelText("Expiry (seconds)"))
  await user.type(screen.getByLabelText("Expiry (seconds)"), expiry)
  await user.click(screen.getByRole("button", { name: "Create key" }))
  expect((await screen.findByRole("alert")).textContent).toBe(message)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(kyPlanBodies(calls)).toEqual([])
})

test("a failed creation stays in the change dialog and shows no key", async () => {
  kyRender({ [KY_APPLY]: kyChange({ state: "needs_repair", output: null, error: { code: "core.needs_repair", message: "the key may exist at Tailscale", cause: "cloud.uncertain_outcome" } }) })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("checkbox", { name: "tag:server" }))
  await user.click(screen.getByRole("button", { name: "Create key" }))
  const plan = await screen.findByRole("dialog", { name: "Create an auth key" })
  const apply = await within(plan).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  expect((await within(plan).findByRole("alert")).textContent).toContain("the key may exist at Tailscale")
  expect(screen.queryByRole("dialog", { name: "Auth key created" })).toBeNull()
})

test("an answer without the key says it was not returned", async () => {
  kyRender({ [KY_APPLY]: kyChange({ output: null }) })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("checkbox", { name: "tag:server" }))
  await user.click(screen.getByRole("button", { name: "Create key" }))
  const plan = await screen.findByRole("dialog", { name: "Create an auth key" })
  const apply = await within(plan).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  const done = await screen.findByRole("dialog", { name: "Auth key created" })
  expect(within(done).getByRole("alert").textContent).toBe("The key was created, but the controller did not return it.")
})

test("without a connected account there is no form, only a link to connect one", async () => {
  kyRender({ [KY_ACCOUNT]: { body: { output: { connected: false } satisfies TailnetAccountStatus } } })
  const link = await screen.findByRole("link", { name: "Connect an account" })
  expect(link.getAttribute("href")).toBe("/tailnet")
  expect(screen.queryByRole("button", { name: "Create key" })).toBeNull()
  expect(await screen.findByRole("table", { name: "Auth keys" })).toBeTruthy()
})

test("a failed list read shows its message", async () => {
  kyRender({ [KY_KEYS]: { status: 500, body: { type: "about:blank", title: "Internal error", status: 500, detail: "the controller database is locked", code: "internal" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("the controller database is locked")
})

test("the page has no accessibility violations", async () => {
  const { container } = kyRender()
  await screen.findByRole("table", { name: "Auth keys" })
  await screen.findByRole("checkbox", { name: "tag:server" })
  expect(await axe(container)).toHaveNoViolations()
})
