import { lazy } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Info } from "lucide-react"
import { expect, test } from "vitest"
import type { Change, Host, Plan } from "@/api/types"
import type { ModuleEntry } from "@/modules/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { CreateHostDialog } from "./create-host-dialog"
import { HostsPage } from "./hosts"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const PLAN = "POST /api/v1/actions/core.host.create/v1/plan"
const APPLY = "POST /api/v1/actions/core.host.create/v1/apply"

function chdHost(modules: Host["modules"]): Host {
  return { id: "h1", name: "controller", isLocal: true, online: true, modules, ingestion: null }
}

const chdPlan: Plan = {
  planId: "p1", hostId: null, action: "core.host.create", version: 1, inputHash: "h",
  diff: [{ op: "+", text: "host web-2: pending" }], guards: [{ id: "name-free", ok: true, message: "no host is named web-2", overridable: false, overridden: false }],
  expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
}

function chdChange(output: unknown, over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: null, action: "core.host.create", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

const chdGrant = { host: { id: "h2", name: "web-2" }, token: "dpe_example_enrollment", expiresAt: "2026-10-23T09:00:00Z" }

// chdJoin stands in for a module's host creation inputs.
const chdJoin = lazy(async () => ({
  default: ({ onChange }: { value: Record<string, unknown>; onChange: (v: Record<string, unknown>) => void }) => (
    <button type="button" onClick={() => onChange({ tailnetJoin: true, name: "ignored" })}>
      Join the tailnet
    </button>
  ),
}))

const chdEntries: ModuleEntry[] = [
  { id: "chdjoin", label: "Join", icon: Info, routes: lazy(async () => ({ default: () => null })), hostCreate: { id: "chdjoin.join", render: chdJoin } },
]

async function chdFill(user: ReturnType<typeof userEvent.setup>, name: string) {
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(false))
  await user.click(screen.getByRole("button", { name: "Add host" }))
  const form = screen.getByRole("dialog")
  await user.type(within(form).getByLabelText("Name"), name)
  return form
}

test("the hosts page adds a host and shows its enrollment token once", async () => {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([])] } } },
    [PLAN]: { body: chdPlan },
    [APPLY]: { body: chdChange(chdGrant) },
  })
  renderWithProviders(<HostsPage />)
  const user = userEvent.setup()
  await screen.findByRole("link", { name: "controller" })
  const form = await chdFill(user, "web-2")
  await user.click(within(form).getByRole("button", { name: "Continue" }))

  const plan = await screen.findByRole("dialog", { name: "Add a host" })
  expect((await within(plan).findByLabelText("Planned changes")).textContent).toBe("+ host web-2: pending")
  await user.click(within(plan).getByRole("button", { name: "Apply" }))

  const done = await screen.findByRole("dialog", { name: "Host added" })
  expect(within(done).getByDisplayValue("dpe_example_enrollment")).toBeTruthy()
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { name: "web-2" } })
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("core.host.list/v1/read")).length).toBeGreaterThan(1))

  await user.click(within(done).getByRole("button", { name: "Done" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Add host" }))
  expect(screen.queryByDisplayValue("dpe_example_enrollment")).toBeNull()
})

test("module inputs of modules enabled on the controller's host merge into the input", async () => {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([{ id: "chdjoin", version: 1, enabled: true, available: true, reason: null, checkedAt: null }])] } } },
    [PLAN]: { body: chdPlan },
  })
  renderWithProviders(<CreateHostDialog entries={chdEntries} />)
  const user = userEvent.setup()
  const form = await chdFill(user, "web-2")
  await user.type(within(form).getByLabelText("Controller address (optional)"), "10.0.0.1:7443")
  await user.click(await within(form).findByRole("button", { name: "Join the tailnet" }))
  await user.click(within(form).getByRole("button", { name: "Continue" }))
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({
      input: { tailnetJoin: true, name: "web-2", controllerAddress: "10.0.0.1:7443" },
    }),
  )
})

test("a module that is not enabled on the controller's host adds no inputs", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [chdHost([{ id: "chdjoin", version: 1, enabled: false, available: true, reason: null, checkedAt: null }])] } } } })
  renderWithProviders(<CreateHostDialog entries={chdEntries} />)
  const user = userEvent.setup()
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" })).toBeTruthy())
  const form = await chdFill(user, "web-2")
  expect(within(form).queryByRole("button", { name: "Join the tailnet" })).toBeNull()
})

test("a failed creation stays in the change dialog with its error and shows no token", async () => {
  stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([])] } } },
    [PLAN]: { body: chdPlan },
    [APPLY]: { body: chdChange(null, { state: "failed", error: { code: "internal", message: "the authority is not loaded" } }) },
  })
  renderWithProviders(<CreateHostDialog />)
  const user = userEvent.setup()
  const form = await chdFill(user, "web-2")
  await user.click(within(form).getByRole("button", { name: "Continue" }))
  const plan = await screen.findByRole("dialog", { name: "Add a host" })
  await user.click(await within(plan).findByRole("button", { name: "Apply" }))
  expect((await within(plan).findByRole("alert")).textContent).toBe("the authority is not loaded")
  expect(screen.queryByRole("dialog", { name: "Host added" })).toBeNull()
})

test("a created host whose answer carries no token says so", async () => {
  stubFetch({
    [HOSTS]: { body: { output: { hosts: [chdHost([])] } } },
    [PLAN]: { body: chdPlan },
    [APPLY]: { body: chdChange({ host: { id: "h2", name: "web-2" } }) },
  })
  renderWithProviders(<CreateHostDialog />)
  const user = userEvent.setup()
  const form = await chdFill(user, "web-2")
  await user.click(within(form).getByRole("button", { name: "Continue" }))
  const plan = await screen.findByRole("dialog", { name: "Add a host" })
  await user.click(await within(plan).findByRole("button", { name: "Apply" }))
  const done = await screen.findByRole("dialog", { name: "Host added" })
  expect(within(done).getByRole("alert").textContent).toBe("The host was created, but the controller did not return its enrollment token.")
})

test("hosts cannot be added until the host list has loaded", async () => {
  const problem = { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" }
  const calls = stubFetch({ [HOSTS]: { status: 503, body: problem } })
  renderWithProviders(<CreateHostDialog entries={chdEntries} />)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("core.host.list/v1/read"))).toBe(true))
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(true))
  await userEvent.setup().click(screen.getByRole("button", { name: "Add host" }))
  expect(screen.queryByRole("dialog")).toBeNull()
})
