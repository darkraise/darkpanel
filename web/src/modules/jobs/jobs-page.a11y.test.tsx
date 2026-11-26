import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { jobsDefault, jobsEmpty, jobsWorst, scheduleNext } from "@/modules/m11-fixtures"
import { expectNoAxeViolations } from "@/test/a11y"
import { makePlan, renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { JobsPage } from "./jobs-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const THEMES = ["light", "dark"] as const
const ok = (output: unknown): StubResponse => ({ body: { output } })
const badSchedule: StubHandler = (c) =>
  (c.body as { input: { schedule: string } }).input.schedule === "*-*-* 25:00:00"
    ? { status: 422, body: { type: "about:blank", title: "bad", status: 422, detail: "systemd cannot read this schedule: hour 25 is out of range", code: "request.invalid_input" } }
    : ok(scheduleNext)

async function menu(item: string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Actions for db-backup" }))
  await user.click(screen.getByRole("menuitem", { name: item }))
}

// Each state: the list answer, the caller's scope and how to reach it.
const STATES: [string, StubHandler, Scope, () => Promise<void>][] = [
  ["loading", () => new Promise(() => {}), "admin", async () => {
    await screen.findByRole("status", { name: "Loading the jobs" })
  }],
  ["error", { status: 500, body: { code: "internal", detail: "boom", status: 500 } }, "admin", async () => {
    await screen.findByText("Couldn't load the jobs")
  }],
  ["empty", ok(jobsEmpty), "admin", async () => {
    await screen.findByText("No Darkpanel jobs yet")
  }],
  ["default", ok(jobsDefault), "admin", async () => {
    await screen.findByRole("table", { name: "Darkpanel jobs" })
  }],
  ["viewer", ok(jobsDefault), "read", async () => {
    await screen.findByRole("table", { name: "Darkpanel jobs" })
  }],
  ["worst case", ok({ ...jobsWorst, jobs: jobsWorst.jobs.slice(0, 30) }), "admin", async () => {
    await screen.findByRole("table", { name: "Darkpanel jobs" })
  }],
  ["form with an invalid schedule", ok(jobsDefault), "admin", async () => {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add job" }))
    await user.type(screen.getByLabelText("Schedule"), "*-*-* 25:00:00")
    await screen.findByText("systemd cannot read this schedule: hour 25 is out of range", {}, { timeout: 3000 })
  }],
  ["form with next runs", ok(jobsDefault), "admin", async () => {
    await menu("Edit")
    await screen.findByRole("list", { name: "Next runs" }, { timeout: 3000 })
  }],
  ["run now dialog", ok(jobsDefault), "admin", async () => {
    await menu("Run now")
    await screen.findByRole("button", { name: "Apply" })
  }],
  ["output", ok(jobsDefault), "admin", async () => {
    await menu("View output")
    await screen.findByRole("region", { name: "Output of darkpanel-job-db-backup.service" })
  }],
]

afterEach(() => {
  document.documentElement.classList.remove("dark")
  vi.unstubAllGlobals()
})

describe("no axe violations in any state", () => {
  for (const theme of THEMES) {
    test.each(STATES)(`%s (${theme})`, async (_name, list, scope, reach) => {
      document.documentElement.classList.toggle("dark", theme === "dark")
      stubFetch({
        [`${BASE}/jobs.job.list/v1/read`]: list,
        [`${BASE}/jobs.schedule.check/v1/read`]: badSchedule,
        [`${BASE}/users.user.list/v1/read`]: ok({ accounts: [] }),
        [`${BASE}/jobs.job.run/v1/plan`]: { body: makePlan("jobs.job.run", { hostId: "h1" }) },
        [`${BASE}/services.journal.query/v1/read`]: ok({ entries: [] }),
        "GET /api/v1/hosts/h1/modules/jobs/availability": { body: { module: "jobs", available: true, reason: null, actions: [] } },
        "GET /api/v1/hosts": { body: { hosts: [] } },
      })
      const { container } = renderWithProviders(<JobsPage hostId="h1" />, { hostScope: scope })
      await reach()
      await expectNoAxeViolations(container)
      for (const dialog of document.querySelectorAll("[role=dialog]")) await expectNoAxeViolations(dialog)
    }, 20_000)
  }
})
