import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { DeployDialog } from "./deploy-dialog"

const DD_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const DD_PLAN = "POST /api/v1/actions/derp.server.deploy/v1/plan"

const ddHosts: StubResponse = {
  body: {
    output: {
      hosts: [
        { id: "h1", name: "controller", isLocal: true, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [], ingestion: null },
        { id: "h2", name: "web-1", isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [], ingestion: null },
      ],
    },
  },
}

function ddPlan(over: Partial<Plan> = {}): StubResponse {
  return {
    body: {
      planId: "p1",
      hostId: null,
      action: "derp.server.deploy",
      version: 1,
      inputHash: "h",
      diff: [{ op: "~", text: "deploy a DERP server" }],
      guards: [],
      expiresAt: "2026-11-02T13:00:00Z",
      confirmation: null,
      ...over,
    },
  }
}

function ddPlans(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("derp.server.deploy/v1/plan")).map((c) => c.body)
}

const DD_OFF = "Client verification is off: any node that knows the region can relay through it."

async function ddOpen() {
  const user = userEvent.setup()
  const dialog = await screen.findByRole("dialog", { name: "Deploy a DERP server" })
  await within(dialog).findByRole("radio", { name: "web-1" })
  return { user, dialog }
}

test("offers the hosts, keeps Review off until a host and a name are given, and plans a Let's Encrypt deployment", async () => {
  const calls = stubFetch({ [DD_HOSTS]: ddHosts, [DD_PLAN]: ddPlan() })
  renderWithProviders(<DeployDialog onClose={() => {}} />)
  const { user, dialog } = await ddOpen()
  const review = within(dialog).getByRole("button", { name: "Review" })
  expect(review.hasAttribute("disabled")).toBe(true)
  expect(within(dialog).getByRole("radio", { name: "controller" })).toBeTruthy()
  expect(within(dialog).getByRole("radio", { name: "Let's Encrypt", checked: true })).toBeTruthy()

  await user.click(within(dialog).getByRole("radio", { name: "web-1" }))
  expect(review.hasAttribute("disabled")).toBe(true)
  await user.type(within(dialog).getByLabelText("DNS name"), "  derp.example.com ")
  await waitFor(() => expect(review.hasAttribute("disabled")).toBe(false))
  await user.click(review)

  const change = await screen.findByRole("dialog", { name: "Deploy a DERP server on web-1" })
  expect((await within(change).findByLabelText("Planned changes")).textContent).toBe("~ deploy a DERP server")
  expect(screen.queryByRole("dialog", { name: "Deploy a DERP server" })).toBeNull()
  expect(ddPlans(calls)).toEqual([{ input: { hostId: "h2", mode: "letsencrypt", hostName: "derp.example.com" } }])
})

test("an IP certificate asks for the public address", async () => {
  const calls = stubFetch({ [DD_HOSTS]: ddHosts, [DD_PLAN]: ddPlan() })
  renderWithProviders(<DeployDialog onClose={() => {}} />)
  const { user, dialog } = await ddOpen()
  await user.click(within(dialog).getByRole("radio", { name: "web-1" }))
  await user.click(within(dialog).getByRole("radio", { name: "IP certificate" }))
  expect(within(dialog).queryByLabelText("DNS name")).toBeNull()
  await user.type(within(dialog).getByLabelText("Public IP address"), "203.0.113.10")
  await user.click(within(dialog).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Deploy a DERP server on web-1" })
  expect(ddPlans(calls)).toEqual([{ input: { hostId: "h2", mode: "ip", hostName: "203.0.113.10" } }])
})

test("client verification is on by default, and clearing it warns and sends verifyClients false", async () => {
  const calls = stubFetch({ [DD_HOSTS]: ddHosts, [DD_PLAN]: ddPlan() })
  renderWithProviders(<DeployDialog onClose={() => {}} />)
  const { user, dialog } = await ddOpen()
  expect(within(dialog).getByRole("checkbox", { name: "Verify clients", checked: true })).toBeTruthy()
  expect(screen.queryByText(DD_OFF)).toBeNull()

  await user.click(within(dialog).getByRole("checkbox", { name: "Verify clients" }))
  expect(screen.getByText(DD_OFF)).toBeTruthy()
  await user.click(within(dialog).getByRole("checkbox", { name: "Verify clients" }))
  expect(screen.queryByText(DD_OFF)).toBeNull()

  await user.click(within(dialog).getByRole("checkbox", { name: "Verify clients" }))
  await user.click(within(dialog).getByRole("radio", { name: "web-1" }))
  await user.type(within(dialog).getByLabelText("DNS name"), "derp.example.com")
  await user.click(within(dialog).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Deploy a DERP server on web-1" })
  expect(ddPlans(calls)).toEqual([{ input: { hostId: "h2", mode: "letsencrypt", hostName: "derp.example.com", verifyClients: false } }])
})

test("closing the change dialog closes the whole flow", async () => {
  const onClose = vi.fn()
  stubFetch({ [DD_HOSTS]: ddHosts, [DD_PLAN]: ddPlan() })
  renderWithProviders(<DeployDialog onClose={onClose} />)
  const { user, dialog } = await ddOpen()
  await user.click(within(dialog).getByRole("radio", { name: "web-1" }))
  await user.type(within(dialog).getByLabelText("DNS name"), "derp.example.com")
  await user.click(within(dialog).getByRole("button", { name: "Review" }))
  const change = await screen.findByRole("dialog", { name: "Deploy a DERP server on web-1" })
  await within(change).findByLabelText("Planned changes")
  expect(onClose).not.toHaveBeenCalled()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
})

test("Cancel closes the dialog and plans nothing", async () => {
  const onClose = vi.fn()
  const calls = stubFetch({ [DD_HOSTS]: ddHosts, [DD_PLAN]: ddPlan() })
  renderWithProviders(<DeployDialog onClose={onClose} />)
  const { user, dialog } = await ddOpen()
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(onClose).toHaveBeenCalledWith()
  expect(ddPlans(calls)).toEqual([])
})

test("a failed host list is shown and nothing can be reviewed", async () => {
  stubFetch({
    [DD_HOSTS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the controller cannot list hosts", code: "unavailable" } },
  })
  renderWithProviders(<DeployDialog onClose={() => {}} />)
  const dialog = await screen.findByRole("dialog", { name: "Deploy a DERP server" })
  expect((await within(dialog).findByRole("alert")).textContent).toBe("the controller cannot list hosts")
  expect(within(dialog).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)
})

test("the form has no accessibility violations", async () => {
  stubFetch({ [DD_HOSTS]: ddHosts })
  renderWithProviders(<DeployDialog onClose={() => {}} />)
  const { dialog } = await ddOpen()
  expect(await axe(dialog)).toHaveNoViolations()
})
