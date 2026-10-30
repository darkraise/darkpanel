import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { dkContainer, dkImage } from "./fixtures"
import DockerRoutes from "./routes"

const DP_BASE = "/api/v1/hosts/h1/resources/docker"

function dpRender(path: string) {
  const calls = stubFetch({
    [`GET ${DP_BASE}/containers`]: { body: { containers: [dkContainer({ id: "c1", name: "web" })] } },
    [`GET ${DP_BASE}/images`]: { body: { images: [dkImage({ tags: ["nginx:1.27"] })] } },
    [`GET ${DP_BASE}/networks`]: { body: { networks: [] } },
    [`GET ${DP_BASE}/volumes`]: { body: { volumes: [] } },
    [`GET ${DP_BASE}/projects`]: { body: { projects: [] } },
  })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/docker/*" element={<DockerRoutes hostId="h1" />} />
    </Routes>,
    { path },
  )
  return { calls, ...result }
}

test("the page opens on the containers tab and lists the five tabs", async () => {
  dpRender("/hosts/h1/docker")
  expect((await screen.findByRole("table", { name: "Docker containers" })).tagName).toBe("TABLE")
  expect(within(screen.getByRole("tablist")).getAllByRole("tab").map((t) => t.textContent)).toEqual(["Containers", "Images", "Networks", "Volumes", "Projects"])
  expect(screen.getByRole("tab", { name: "Containers", selected: true })).toBeTruthy()
  expect(screen.queryByRole("table", { name: "Docker images" })).toBeNull()
})

test("the Images tab shows the images", async () => {
  dpRender("/hosts/h1/docker")
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Images" }))
  const rows = within(await screen.findByRole("table", { name: "Docker images" })).getAllByRole("row")
  expect(rows).toHaveLength(2)
  expect(screen.queryByRole("table", { name: "Docker containers" })).toBeNull()
})

test("the Networks, Volumes and Projects tabs read their resources when opened", async () => {
  const { calls } = dpRender("/hosts/h1/docker")
  const user = userEvent.setup()
  await screen.findByRole("table", { name: "Docker containers" })
  for (const [tab, resource] of [["Networks", "networks"], ["Volumes", "volumes"], ["Projects", "projects"]] as const) {
    await user.click(screen.getByRole("tab", { name: tab }))
    await waitFor(() => expect(calls.some((c) => c.method === "GET" && c.path === `${DP_BASE}/${resource}`)).toBe(true))
  }
})

test("an unknown subpage is answered", () => {
  dpRender("/hosts/h1/docker/nope")
  expect(screen.getByText("Page not found").textContent).toBe("Page not found")
})

test("the Docker page has no accessibility violations", async () => {
  const { container } = dpRender("/hosts/h1/docker")
  await screen.findByRole("table", { name: "Docker containers" })
  expect(await axe(container)).toHaveNoViolations()
})
