import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { dkProject } from "./fixtures"
import { ProjectsTab } from "./projects-tab"
import type { DockerProject } from "./types"

const PT_LIST = "GET /api/v1/hosts/h1/resources/docker/projects"
const PT_RAN = "2026-10-29T12:00:00Z"

function ptPlan(call: FetchCall) {
  const action = call.path.split("/")[6] ?? ""
  return { body: { planId: "p1", hostId: "h1", action, version: 1, inputHash: "h", diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null } }
}

function ptProjects(): DockerProject[] {
  return [
    dkProject({
      name: "blog", directory: "/srv/blog", files: ["/srv/blog/compose.yaml", "/srv/blog/compose.prod.yaml"], envFiles: ["/srv/blog/.env"],
      profiles: ["web"], configHash: "abc123", hashChanged: true, containers: ["blog-web-1", "blog-db-1"], running: 1, lastRun: PT_RAN,
    }),
    dkProject({
      name: "mail", directory: "/srv/mail", files: ["/srv/mail/compose.yaml"], envFiles: [], profiles: [], configHash: "", hashChanged: false,
      containers: [], running: 0, lastRun: undefined,
    }),
  ]
}

function ptRender(projects: DockerProject[] = ptProjects(), routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [PT_LIST]: { body: { projects } },
    "GET /api/v1/hosts/h1/modules/docker/availability": { body: { module: "docker", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/docker.compose.up/v1/plan": ptPlan,
    "POST /api/v1/hosts/h1/actions/docker.compose.down/v1/plan": ptPlan,
    "POST /api/v1/hosts/h1/actions/docker.compose.pull/v1/plan": ptPlan,
    "POST /api/v1/hosts/h1/actions/docker.compose.restart/v1/plan": ptPlan,
    ...routes,
  })
  renderWithProviders(<ProjectsTab hostId="h1" />)
  return calls
}

async function ptProject(name: string): Promise<HTMLElement> {
  return screen.findByRole("region", { name: `Project ${name}` })
}

function ptValue(section: HTMLElement, label: string): string | null | undefined {
  return within(section).getByText(label, { selector: "dt" }).nextElementSibling?.textContent
}

function ptPlanned(calls: FetchCall[]): { path: string; body: unknown }[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => ({ path: c.path, body: c.body }))
}

test("shows each project's files, containers and last run", async () => {
  ptRender()
  const blog = await ptProject("blog")
  expect(ptValue(blog, "Directory")).toBe("/srv/blog")
  expect(ptValue(blog, "Compose files")).toBe("/srv/blog/compose.yaml, /srv/blog/compose.prod.yaml")
  expect(ptValue(blog, "Env files")).toBe("/srv/blog/.env")
  expect(ptValue(blog, "Containers")).toBe("blog-web-1, blog-db-1")
  expect(ptValue(blog, "Last run")).toBe(formatTime(PT_RAN))
  expect(within(blog).getByText("1 of 2 running").textContent).toBe("1 of 2 running")
  const mail = await ptProject("mail")
  expect(ptValue(mail, "Env files")).toBe("none")
  expect(ptValue(mail, "Containers")).toBe("none")
  expect(ptValue(mail, "Last run")).toBe("never")
})

test("a changed hash and unreadable files are called out", async () => {
  ptRender()
  const blog = await ptProject("blog")
  expect(within(blog).getByText("The Compose files changed since the last run.").textContent).toBe("The Compose files changed since the last run.")
  expect(within(blog).queryByText("The Compose files cannot be read.")).toBeNull()
  const mail = await ptProject("mail")
  expect(within(mail).getByText("The Compose files cannot be read.").textContent).toBe("The Compose files cannot be read.")
  expect(within(mail).queryByText("The Compose files changed since the last run.")).toBeNull()
})

test("says so when no Compose project was found", async () => {
  ptRender([])
  expect((await screen.findByText("No Compose projects")).textContent).toBe("No Compose projects")
})

test("shows the reason when the projects cannot be read", async () => {
  ptRender([], {
    [PT_LIST]: { status: 503, body: { type: "about:blank", title: "Docker unavailable", status: 503, detail: "the Docker daemon is not reachable", code: "docker.engine_unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the Docker daemon is not reachable")
})

test.each([
  ["Up", "docker.compose.up", "Start project blog"],
  ["Down", "docker.compose.down", "Take down project blog"],
  ["Pull", "docker.compose.pull", "Pull images of project blog"],
  ["Restart", "docker.compose.restart", "Restart project blog"],
])("%s plans %s with the project name and leaves the stored profiles alone", async (button, action, title) => {
  const calls = ptRender()
  await ptProject("blog")
  await userEvent.setup().click(screen.getByRole("button", { name: `${button} blog` }))
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  expect(ptPlanned(calls)).toEqual([{ path: `/api/v1/hosts/h1/actions/${action}/v1/plan`, body: { input: { project: "blog" } } }])
})

test.each([
  ["a new list", "web, worker", ["web", "worker"]],
  ["extra spaces and an empty entry", "  web ,, worker  ", ["web", "worker"]],
  ["an emptied field", "", []],
])("%s typed in Profiles replaces the stored list in the input", async (_name, typed, want) => {
  const calls = ptRender()
  const blog = await ptProject("blog")
  const user = userEvent.setup()
  const field = within(blog).getByLabelText("Profiles")
  expect((field as HTMLInputElement).value).toBe("web")
  await user.clear(field)
  if (typed !== "") await user.type(field, typed)
  await user.click(within(blog).getByRole("button", { name: "Restart blog" }))
  await within(await screen.findByRole("dialog", { name: "Restart project blog" })).findByLabelText("Planned changes")
  expect(ptPlanned(calls).map((c) => c.body)).toEqual([{ input: { project: "blog", profiles: want } }])
})

test("typing the stored list again sends no profiles", async () => {
  const calls = ptRender()
  const blog = await ptProject("blog")
  const user = userEvent.setup()
  const field = within(blog).getByLabelText("Profiles")
  await user.clear(field)
  await user.type(field, "web")
  await user.click(within(blog).getByRole("button", { name: "Pull blog" }))
  await within(await screen.findByRole("dialog", { name: "Pull images of project blog" })).findByLabelText("Planned changes")
  expect(ptPlanned(calls).map((c) => c.body)).toEqual([{ input: { project: "blog" } }])
})

test("the projects have no accessibility violations", async () => {
  ptRender()
  await ptProject("blog")
  expect(await axe(screen.getByRole("list", { name: "Compose projects" }))).toHaveNoViolations()
})
