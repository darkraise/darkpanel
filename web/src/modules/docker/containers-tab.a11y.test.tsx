import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler, type StubResponse } from "@/test/utils"
import { ContainersTab } from "./containers-tab"
import { dkContainer } from "./fixtures"
import type { DockerContainer } from "./types"

const CA_LIST = "GET /api/v1/hosts/h1/resources/docker/containers"
const CA_ACTIONS = ["docker.container.start", "docker.container.stop", "docker.container.restart", "docker.container.remove"]
const CA_DONE = { id: "ch1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }
const CA_BUSY = { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "the container is busy", code: "docker.busy" } }

function caPlan(call: FetchCall): StubResponse {
  const action = call.path.split("/")[6] ?? ""
  const confirmation = action === "docker.container.remove" ? "db" : null
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation } }
}

const caWeb = dkContainer({ id: "c1", name: "web", state: "running", status: "Up 2 hours", owner: undefined })
const caDb = dkContainer({ id: "c2", name: "db", image: "postgres:17", state: "exited", status: "Exited (0) 3 days ago", ports: [], owner: undefined, usage: undefined })

// caRender serves whatever list read() returns at the time of each request, so a test changes the
// host's containers between the apply and the refetch that follows the dialog's close.
function caRender(read: () => DockerContainer[], routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [CA_LIST]: () => ({ body: { containers: read() } }),
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: true, reason: null, actions: [] } },
    "GET /api/v1/changes/ch1": { body: CA_DONE },
    ...Object.fromEntries(CA_ACTIONS.map((a) => [`POST /api/v1/hosts/h1/actions/${a}/v1/plan`, caPlan])),
    ...Object.fromEntries(CA_ACTIONS.map((a) => [`POST /api/v1/hosts/h1/actions/${a}/v1/apply`, { body: CA_DONE }])),
    ...routes,
  })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<ContainersTab hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/docker" },
  )
  return { listReads: () => calls.filter((c) => c.method === "GET" && c.path === "/api/v1/hosts/h1/resources/docker/containers").length, ...result }
}

async function caApply(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement) {
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await within(dialog).findByText("completed")
}

const caViews: { state: string; containers: DockerContainer[]; routes: Record<string, StubHandler>; ready: () => Promise<unknown> }[] = [
  { state: "the table", containers: [caWeb, caDb], routes: {}, ready: () => screen.findByRole("table", { name: "Docker containers" }) },
  { state: "the empty state", containers: [], routes: {}, ready: () => screen.findByText("No containers") },
  { state: "the read error", containers: [], routes: { [CA_LIST]: CA_BUSY }, ready: () => screen.findByRole("alert") },
]

test.each(caViews)("$state has no axe violations", async ({ containers, routes, ready }) => {
  const { container } = caRender(() => containers, routes)
  await ready()
  await expectNoAxeViolations(container)
})

const caDialogs: { state: string; label: string; routes: Record<string, StubHandler>; ready: string | null }[] = [
  { state: "planning", label: "Restart web", routes: { "POST /api/v1/hosts/h1/actions/docker.container.restart/v1/plan": () => new Promise<StubResponse>(() => {}) }, ready: "Planning the change" },
  { state: "the plan", label: "Restart web", routes: {}, ready: "Planned changes" },
  { state: "a plan error", label: "Stop web", routes: { "POST /api/v1/hosts/h1/actions/docker.container.stop/v1/plan": CA_BUSY }, ready: null },
  { state: "the typed confirmation", label: "Remove db", routes: {}, ready: "Type db to confirm" },
]

test.each(caDialogs)("the dialog in $state has no axe violations", async ({ label, routes, ready }) => {
  caRender(() => [caWeb, caDb], routes)
  await userEvent.setup().click(await screen.findByRole("button", { name: label }))
  const dialog = await screen.findByRole("dialog", { name: label })
  if (ready === null) await within(dialog).findByText("the container is busy")
  else await within(dialog).findByLabelText(ready)
  await expectNoAxeViolations(dialog)
})

test("the dialog of an applied change has no axe violations", async () => {
  caRender(() => [caWeb, caDb])
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Restart web" }))
  const dialog = await screen.findByRole("dialog", { name: "Restart web" })
  await caApply(user, dialog)
  await expectNoAxeViolations(dialog)
})

test("Tab reaches every enabled row action, named for its container, in row order", async () => {
  caRender(() => [caWeb, caDb])
  await screen.findByRole("table", { name: "Docker containers" })
  expect(await tabSequence(userEvent.setup(), 6)).toEqual(["web", "Stop web", "Restart web", "db", "Start db", "Remove db"])
})

test.each(["Stop web", "Restart web", "Start db", "Remove db"])(
  "%s opens a dialog that keeps Tab inside it and Escape gives focus back to the action after the table re-renders",
  async (label) => {
    const { listReads } = caRender(() => [caWeb, caDb])
    const user = userEvent.setup()
    ;(await screen.findByRole("button", { name: label })).focus()
    await user.keyboard("{Enter}")
    const dialog = await screen.findByRole("dialog", { name: label })
    await within(dialog).findByLabelText("Planned changes")
    expect(dialog.contains(document.activeElement)).toBe(true)
    for (let i = 0; i < 6; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement)).toBe(true)
    }
    const before = listReads()
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await waitFor(() => expect(listReads()).toBeGreaterThan(before))
    expect(document.activeElement).toBe(screen.getByRole("button", { name: label }))
  },
)

test("when an applied Stop leaves the row without that action, focus moves to the row's first enabled action", async () => {
  let running = true
  caRender(() => [running ? caWeb : { ...caWeb, state: "exited", status: "Exited (0) 1 second ago" }, caDb])
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Stop web" }))
  const dialog = await screen.findByRole("dialog", { name: "Stop web" })
  running = false
  await caApply(user, dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(focusedName()).toBe("Start web"))
})

test("a read that lands after the close's read failed leaves focus where the user put it", async () => {
  let fail = false
  const { client } = caRender(() => [caWeb, caDb], { [CA_LIST]: () => (fail ? CA_BUSY : { body: { containers: [caWeb, caDb] } }) })
  const outside = document.createElement("button")
  outside.textContent = "Elsewhere"
  document.body.append(outside)
  try {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Restart web" }))
    await within(await screen.findByRole("dialog", { name: "Restart web" })).findByLabelText("Planned changes")
    fail = true
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await screen.findByRole("alert")
    outside.focus()
    fail = false
    await act(() => client.invalidateQueries())
    await screen.findByRole("table", { name: "Docker containers" })
    expect(document.activeElement).toBe(outside)
  } finally {
    outside.remove()
  }
})

test("when an applied Remove deletes the row, focus moves to the containers group", async () => {
  let rows = [caWeb, caDb]
  caRender(() => rows)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Remove db" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove db" })
  await user.type(await within(dialog).findByLabelText("Type db to confirm"), "db")
  rows = [caWeb]
  await caApply(user, dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(screen.queryByRole("button", { name: "Remove db" })).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("group", { name: "Containers" })))
})
