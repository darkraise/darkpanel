import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState } from "react"
import { expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { DockerChange } from "./docker-change"

const DA_ACTION = "docker.compose.down"
const DA_BASE = "/api/v1/hosts/h1"
const DA_PLAN_PATH = `POST ${DA_BASE}/actions/${DA_ACTION}/v1/plan`
const DA_DONE = { id: "ch1", state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function daPlan(confirmation: string | null): StubResponse {
  return { body: { planId: "p1", hostId: "h1", action: DA_ACTION, version: 1, inputHash: "h", diff: [{ op: "-", text: "project blog" }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation } }
}

// DaHarness is the trigger a page would own: DockerChange stays mounted and opens while its input is set.
function DaHarness() {
  const [open, setOpen] = useState(false)
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>
        Open change
      </button>
      <DockerChange hostId="h1" action={DA_ACTION} title="Take blog down" input={open ? { project: "blog" } : null} onClose={() => setOpen(false)} />
    </>
  )
}

const daStates: { state: string; routes: Record<string, StubHandler>; ready: (dialog: HTMLElement) => Promise<unknown>; apply?: string }[] = [
  { state: "planning", routes: { [DA_PLAN_PATH]: () => new Promise<StubResponse>(() => {}) }, ready: (d) => within(d).findByLabelText("Planning the change") },
  { state: "the plan", routes: {}, ready: (d) => within(d).findByLabelText("Planned changes") },
  {
    state: "a plan error",
    routes: { [DA_PLAN_PATH]: { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "the project is busy", code: "docker.busy" } } },
    ready: (d) => within(d).findByText("the project is busy"),
  },
  { state: "the typed confirmation", routes: { [DA_PLAN_PATH]: daPlan("blog") }, ready: (d) => within(d).findByLabelText("Type blog to confirm") },
  { state: "the applied change", routes: {}, ready: (d) => within(d).findByText("completed"), apply: "go" },
]

test.each(daStates)("$state has no axe violations, keeps Tab inside the dialog and gives focus back to its trigger", async ({ routes, ready, apply }) => {
  stubFetch({
    [`GET ${DA_BASE}/modules/docker/availability`]: { body: { module: "docker", available: true, reason: null, actions: [] } },
    [DA_PLAN_PATH]: daPlan(null),
    [`POST ${DA_BASE}/actions/${DA_ACTION}/v1/apply`]: { body: DA_DONE },
    "GET /api/v1/changes/ch1": { body: DA_DONE },
    ...routes,
  })
  renderWithProviders(<DaHarness />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open change" }))
  const dialog = await screen.findByRole("dialog", { name: "Take blog down" })
  if (apply !== undefined) {
    const button = await within(dialog).findByRole("button", { name: "Apply" })
    await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false))
    await user.click(button)
  }
  await ready(dialog)
  await expectNoAxeViolations(dialog)
  for (let i = 0; i < 6; i++) {
    await user.tab()
    expect(dialog.contains(document.activeElement)).toBe(true)
  }
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Open change" }))
})

test("a module the host reports unavailable shows why and has no axe violations", async () => {
  stubFetch({
    [`GET ${DA_BASE}/modules/docker/availability`]: { body: { module: "docker", available: false, reason: "the Docker socket is missing", actions: [] } },
    [DA_PLAN_PATH]: daPlan(null),
  })
  renderWithProviders(<DaHarness />)
  await userEvent.setup().click(screen.getByRole("button", { name: "Open change" }))
  const dialog = await screen.findByRole("dialog", { name: "Take blog down" })
  await within(dialog).findByText("the Docker socket is missing")
  await expectNoAxeViolations(dialog)
})
