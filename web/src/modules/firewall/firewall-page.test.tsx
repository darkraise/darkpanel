import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { fwDocker, fwSnapshot, fwStatus } from "./fixtures"
import FirewallRoutes from "./routes"

const STATUS = "GET /api/v1/hosts/h1/resources/firewall/status"
const SNAPSHOTS = "GET /api/v1/hosts/h1/resources/firewall/snapshots"
const DOCKER = "GET /api/v1/hosts/h1/resources/firewall/docker"

function fpRender(path: string, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [STATUS]: { body: fwStatus() },
    [SNAPSHOTS]: { body: { snapshots: [fwSnapshot()] } },
    [DOCKER]: { body: fwDocker({ available: false, reason: "the Docker socket is not present", enabled: false, health: "off" }) },
    ...routes,
  })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/*" element={<FirewallRoutes hostId="h1" />} />
    </Routes>,
    { path },
  )
  return { calls, ...result }
}

test("the firewall page shows the state, the settings and the rules", async () => {
  fpRender("/hosts/h1/firewall")
  expect(await screen.findByLabelText("Configured state")).toBeTruthy()
  expect(screen.getByLabelText("Loaded state")).toBeTruthy()
  expect(screen.getByRole("form", { name: "Default policy" })).toBeTruthy()
  expect(within(screen.getByRole("table", { name: "Firewall rules" })).getAllByRole("row")).toHaveLength(4)
  expect(screen.getByRole("button", { name: "Add rule" })).toBeTruthy()
  expect(screen.queryByRole("alert")).toBeNull()
})

test("the firewall page shows the Docker integration and links to the exposure page", async () => {
  fpRender("/hosts/h1/firewall")
  expect((await screen.findByText(/^Docker integration/)).textContent).toBe("Docker integrationoff")
  expect(screen.getByText("Docker is not available on this host: the Docker socket is not present").textContent).toContain("socket is not present")
  expect(screen.getByRole("link", { name: "Exposure" }).getAttribute("href")).toBe("/hosts/h1/firewall/exposure")
})

test("an unsupported configuration shows its banner on the page", async () => {
  fpRender("/hosts/h1/firewall", { [STATUS]: { body: fwStatus({ supported: false, unsupported: ["/etc/ufw/before.init is executable"] }) } })
  expect(within(await screen.findByRole("alert")).getByText("Unsupported configuration").textContent).toBe("Unsupported configuration")
})

test("a failed status read shows the server's reason", async () => {
  fpRender("/hosts/h1/firewall", {
    [STATUS]: { status: 409, body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the firewall module is unavailable on this host", code: "module.unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the firewall module is unavailable on this host")
})

test("the snapshots link opens the snapshots page and its back link returns", async () => {
  fpRender("/hosts/h1/firewall")
  const user = userEvent.setup()
  await user.click(await screen.findByRole("link", { name: "Snapshots" }))
  expect(await screen.findByRole("table", { name: "Firewall snapshots" })).toBeTruthy()
  expect(screen.getByTestId("location").textContent).toBe("/hosts/h1/firewall/snapshots")
  await user.click(screen.getByRole("link", { name: "Back to the firewall" }))
  await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/hosts/h1/firewall"))
})

test("an unknown subpage is answered", () => {
  fpRender("/hosts/h1/firewall/nope")
  expect(screen.getByText("Page not found").textContent).toBe("Page not found")
})

test("the firewall page has no accessibility violations", async () => {
  const { container } = fpRender("/hosts/h1/firewall")
  await screen.findByRole("table", { name: "Firewall rules" })
  expect(await axe(container)).toHaveNoViolations()
})
