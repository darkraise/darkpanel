import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Toaster } from "darkraise-ui/components/sonner"
import { afterEach, describe, expect, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { jobsDefault, scheduleNext } from "@/modules/m11-fixtures"
import { EvtSource } from "@/test/evt-source"
import { makeChange, makePlan, renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { JobsPage } from "./jobs-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const ok = (output: unknown): StubResponse => ({ body: { output } })
const refused = (detail: string): StubResponse => ({ status: 422, body: { type: "about:blank", title: detail, status: 422, detail, code: "request.invalid_input" } })

function render(scope: Scope = "admin", routes: Record<string, StubHandler> = {}, list: object = jobsDefault) {
  const calls = stubFetch({
    [`${BASE}/jobs.job.list/v1/read`]: ok(list),
    [`${BASE}/users.user.list/v1/read`]: ok({ accounts: [{ name: "backup", uid: 34 }, { name: "deploy", uid: 1000 }] }),
    "GET /api/v1/hosts/h1/modules/jobs/availability": { body: { module: "jobs", available: true, reason: null, actions: [] } },
    "GET /api/v1/hosts": { body: { hosts: [] } },
    ...routes,
  })
  renderWithProviders(
    <>
      <JobsPage hostId="h1" />
      <Toaster />
    </>,
    { hostScope: scope },
  )
  return calls
}

async function pick(slug: string, item: string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: `Actions for ${slug}` }))
  await user.click(screen.getByRole("menuitem", { name: item }))
  return user
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("jobs flows", () => {
  test("the schedule field shows the next three runs, or the agent's error inline", async () => {
    const checked: unknown[] = []
    render("admin", {
      [`${BASE}/jobs.schedule.check/v1/read`]: (c) => {
        checked.push(c.body)
        const input = (c.body as { input: { schedule: string } }).input
        return input.schedule === "*-*-* 25:00:00" ? refused("systemd cannot read this schedule: hour 25 is out of range") : ok(scheduleNext)
      },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add job" }))
    await user.type(screen.getByLabelText("Schedule"), "*-*-* 03:00:00")
    const runs = await screen.findByRole("list", { name: "Next runs" }, { timeout: 3000 })
    expect(within(runs).getAllByRole("listitem")).toHaveLength(3)
    await user.clear(screen.getByLabelText("Schedule"))
    await user.type(screen.getByLabelText("Schedule"), "*-*-* 25:00:00")
    expect(await screen.findByText("systemd cannot read this schedule: hour 25 is out of range", {}, { timeout: 3000 })).toBeTruthy()
    expect(screen.getByLabelText("Schedule").getAttribute("aria-invalid")).toBe("true")
    expect(screen.queryByRole("list", { name: "Next runs" })).toBeNull()
    expect(checked).toContainEqual({ input: { schedule: "*-*-* 25:00:00" } })
  })

  test("Add job plans jobs.job.create with every field", async () => {
    const plans: unknown[] = []
    render("admin", {
      [`${BASE}/jobs.schedule.check/v1/read`]: ok(scheduleNext),
      [`${BASE}/jobs.job.create/v1/plan`]: (c) => {
        plans.push(c.body)
        return { body: makePlan("jobs.job.create", { hostId: "h1" }) }
      },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add job" }))
    await user.type(screen.getByLabelText("Name"), "Bad Name")
    expect(screen.getByText("Use lower case letters, digits and hyphens, at most 40")).toBeTruthy()
    await user.clear(screen.getByLabelText("Name"))
    await user.type(screen.getByLabelText("Name"), "nightly")
    await user.type(screen.getByLabelText("Schedule"), "daily")
    await user.type(screen.getByLabelText("Command"), "echo hi")
    await user.clear(screen.getByLabelText("Timeout (seconds)"))
    await user.type(screen.getByLabelText("Timeout (seconds)"), "30")
    expect(screen.getByText("Use 60 to 86400 seconds")).toBeTruthy()
    await user.clear(screen.getByLabelText("Timeout (seconds)"))
    await user.type(screen.getByLabelText("Timeout (seconds)"), "600")
    await user.click(screen.getByLabelText("Catch up a run missed while the host was off"))
    await screen.findByRole("list", { name: "Next runs" }, { timeout: 3000 })
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByRole("button", { name: "Apply" })
    expect(plans).toEqual([
      { input: { slug: "nightly", description: "", schedule: "daily", user: "root", command: "echo hi", timeoutSeconds: 600, catchUp: true, randomDelaySeconds: 0 } },
    ])
  })

  test("Run now applies jobs.job.run and gives the toast Started", async () => {
    const plans: unknown[] = []
    render("admin", {
      [`${BASE}/jobs.job.run/v1/plan`]: (c) => {
        plans.push(c.body)
        return { body: makePlan("jobs.job.run", { hostId: "h1" }) }
      },
      [`${BASE}/jobs.job.run/v1/apply`]: { body: makeChange("jobs.job.run", { slug: "db-backup" }, { class: "compensable" }) },
    })
    const user = await pick("db-backup", "Run now")
    await user.click(await screen.findByRole("button", { name: "Apply" }))
    await waitFor(() => expect(document.querySelector(".dr-toast-title")?.textContent).toBe("Started"))
    expect(plans).toEqual([{ input: { slug: "db-backup" } }])
  })

  test("View output opens the journal filtered to the job's unit", async () => {
    EvtSource.instances = []
    render()
    await pick("db-backup", "View output")
    const output = await screen.findByRole("region", { name: "Output of darkpanel-job-db-backup.service" })
    await waitFor(() => expect(document.activeElement).toBe(output))
    await waitFor(() => expect(EvtSource.instances.some((e) => e.url.includes("journal-follow"))).toBe(true))
    const follow = new URL(EvtSource.instances.find((e) => e.url.includes("journal-follow"))?.url ?? "", "http://localhost")
    expect(follow.searchParams.get("unit")).toBe("darkpanel-job-db-backup.service")
  })

  // The agent leaves the command text out of a viewer's list
  // (internal/modules/jobs/list_test.go); the page shows what is left.
  test("a viewer's list, sent without command text, shows each job, Cron entry for each cron line and no actions", async () => {
    const list = { ...jobsDefault, jobs: jobsDefault.jobs.map((j) => ({ ...j, command: undefined })), cron: jobsDefault.cron.map((c) => ({ ...c, command: undefined })) }
    render("read", {}, list)
    const table = await screen.findByRole("table", { name: "Darkpanel jobs" })
    expect(within(table).getByText("db-backup")).toBeTruthy()
    expect(within(screen.getByRole("table", { name: "Other scheduled tasks" })).getAllByText("Cron entry")).toHaveLength(jobsDefault.cron.length)
    expect(screen.queryByRole("button", { name: /^Actions for/ })).toBeNull()
    expect(screen.queryByRole("button", { name: "Add job" })).toBeNull()
  })

  test("an admin sees each job's command, and the other scheduled tasks show their source read-only", async () => {
    render()
    expect(await screen.findByText("/usr/local/bin/pg-backup --all")).toBeTruthy()
    const other = screen.getByRole("table", { name: "Other scheduled tasks" })
    for (const t of jobsDefault.timers) expect(within(other).getByText(t.source)).toBeTruthy()
    expect(within(other).getByText("/etc/cron.d/certbot")).toBeTruthy()
    expect(within(other).queryAllByRole("button")).toHaveLength(0)
    expect(screen.getByText("Every day at 03:00")).toBeTruthy()
    expect(screen.getByText("Failed (exit 2)")).toBeTruthy()
    expect(screen.getByText("Disabled")).toBeTruthy()
  })

  test("no jobs shows the empty state with Add job", async () => {
    render("admin", {}, { jobs: [], timers: [], cron: [] })
    expect(await screen.findByText("No Darkpanel jobs yet")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Add job" })).toBeTruthy()
  })
})
