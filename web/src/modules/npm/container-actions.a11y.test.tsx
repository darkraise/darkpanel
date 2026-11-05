import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { dkDetail, dkNetwork } from "@/modules/docker/fixtures"
import type { ContainerRef } from "@/modules/types"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ExposeAction } from "./container-actions"
import { npInstance } from "./fixtures"

const CA_HOST = "/api/v1/hosts/h1"
const CA_STATUS = `POST ${CA_HOST}/actions/npm.instance.status/v1/read`
const CA_INSPECT = `POST ${CA_HOST}/actions/docker.container.inspect/v1/read`
const CA_NETWORKS = `GET ${CA_HOST}/resources/docker/networks`
const CA_PLAN = "POST /api/v1/actions/npm.expose.container/v1/plan"
const CA_DONE = { id: "ch1", state: "completed", class: "composite", error: null, output: null, revertDeadline: null }
const CA_DOWN = { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the NPM module is unavailable", code: "module.unavailable" } }

const caContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

function caDeployed(over: Parameters<typeof npInstance>[0] = {}) {
  return npInstance({ deployed: true, containerId: "c0ffee", running: true, apiReady: true, public: "confirmed", adminEmail: "admin@example.com", ...over })
}

function caRender(routes: Record<string, StubHandler> = {}, networks: string[] = ["bridge", "backend"]) {
  const calls = stubFetch({
    [CA_STATUS]: { body: { output: caDeployed() } },
    [CA_INSPECT]: {
      body: { output: dkDetail({ id: "c1", ports: [{ privatePort: 80, proto: "tcp" }], networks: networks.map((network) => ({ network, networkId: "id-" + network, container: "web" })) }) },
    },
    [CA_NETWORKS]: { body: { networks: networks.map((n) => dkNetwork({ id: "id-" + n, name: n, driver: "bridge" })) } },
    [CA_PLAN]: (call: FetchCall) => ({
      body: { planId: "p1", hostId: null, action: "npm.expose.container", version: 1, inputHash: "h", diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-02T13:00:00Z", confirmation: null },
    }),
    "POST /api/v1/actions/npm.expose.container/v1/apply": { body: CA_DONE },
    "GET /api/v1/changes/ch1": { body: CA_DONE },
    "GET /api/v1/changes/ch1/steps": { body: { steps: [] } },
    ...routes,
  })
  const result = renderWithProviders(<ExposeAction container={caContainer} onDone={() => {}} />)
  return { statusReads: () => calls.filter((c) => c.path === `${CA_HOST}/actions/npm.instance.status/v1/read`).length, ...result }
}

const caViews: { state: string; routes: Record<string, StubHandler>; networks: string[]; ready: () => Promise<unknown> }[] = [
  { state: "the Expose form", routes: {}, networks: ["bridge", "backend"], ready: () => screen.findByRole("form", { name: "Expose container" }) },
  { state: "the form of a container on no user-defined network", routes: {}, networks: ["bridge"], ready: () => screen.findByRole("form", { name: "Expose container" }) },
  { state: "a host without a deployed instance", routes: { [CA_STATUS]: { body: { output: caDeployed({ deployed: false, running: false, apiReady: false, public: "no" }) } } }, networks: ["bridge"], ready: () => screen.findByText(/not deployed/) },
  { state: "a failed status read", routes: { [CA_STATUS]: CA_DOWN }, networks: ["bridge"], ready: () => screen.findByRole("alert") },
]

test.each(caViews)("$state has no axe violations", async ({ routes, networks, ready }) => {
  const { container } = caRender(routes, networks)
  await ready()
  await expectNoAxeViolations(container)
})

test("the form with an invalid port has no axe violations and marks the port invalid", async () => {
  const { container } = caRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Expose container" })
  await user.clear(within(form).getByLabelText("Port"))
  await user.type(within(form).getByLabelText("Port"), "http")
  await expectNoAxeViolations(container)
  expect(within(form).getByLabelText("Port").getAttribute("aria-invalid")).toBe("true")
})

test("the plan and applied dialogs of the exposure have no axe violations", async () => {
  caRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Expose container" })
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  await expectNoAxeViolations(dialog)
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
  await expectNoAxeViolations(dialog)
})

test("Tab goes through every field of the form in reading order with its label as name", async () => {
  caRender()
  await screen.findByRole("form", { name: "Expose container" })
  expect(await tabSequence(userEvent.setup(), 4)).toEqual(["backend", "Port", "Domains", "Websockets"])
})

test("the exposure dialog keeps Tab inside it and Escape gives focus back to Plan after the form re-renders", async () => {
  const { statusReads } = caRender()
  const user = userEvent.setup()
  const form = await screen.findByRole("form", { name: "Expose container" })
  await user.type(within(form).getByLabelText("Domains"), "app.example.com")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(dialog.contains(document.activeElement)).toBe(true)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  const before = statusReads()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(statusReads()).toBeGreaterThan(before))
  expect(document.activeElement).toBe(within(screen.getByRole("form", { name: "Expose container" })).getByRole("button", { name: "Plan" }))
})
