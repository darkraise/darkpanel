import { screen } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { formatTime } from "@/lib/format"
import { renderWithProviders, stubFetch } from "@/test/utils"
import HostinfoRoutes from "./routes"
import { SummaryPage, type Summary } from "./summary-page"

const summary: Summary = {
  hostname: "web-1",
  os: { id: "ubuntu", versionId: "24.04", prettyName: "Ubuntu 24.04.3 LTS" },
  kernel: "6.8.0-45-generic",
  arch: "amd64",
  uptimeSeconds: 273900,
  bootTime: "2026-10-16T03:55:00Z",
  cpu: { model: "AMD EPYC 7763", logicalCount: 4 },
  memory: { totalBytes: 17179869184, availableBytes: 8589934592 },
  swap: { totalBytes: 0, freeBytes: 0 },
  load: { one: 0.5, five: 0.25, fifteen: 0.1 },
  rootFs: { totalBytes: 107374182400, availableBytes: 53687091200 },
  darkpanelVersion: "0.1.0",
}

const SUMMARY_PATH = "GET /api/v1/hosts/h1/resources/hostinfo/summary"

test("shows the summary as labelled, human-readable values", async () => {
  stubFetch({ [SUMMARY_PATH]: { body: summary } })
  renderWithProviders(<SummaryPage hostId="h1" />)
  await screen.findByLabelText("Hostname: web-1")
  screen.getByLabelText("Operating system: Ubuntu 24.04.3 LTS")
  screen.getByLabelText("Kernel: 6.8.0-45-generic")
  screen.getByLabelText("Architecture: amd64")
  screen.getByRole("group", { name: "Uptime: 3 d 4 h 5 min" })
  screen.getByLabelText(`Booted: ${formatTime("2026-10-16T03:55:00Z")}`)
  screen.getByText("AMD EPYC 7763 (4 logical CPUs)")
  screen.getByRole("group", { name: "Load average: 0.50 / 0.25 / 0.10" })
  screen.getByRole("img", { name: "Memory: 8 of 16 GiB (50%)" })
  screen.getByText("No swap")
  screen.getByRole("img", { name: "Disk: 50 of 100 GiB (50%)" })
  screen.getByLabelText("Darkpanel version: 0.1.0")
})

test("shows swap use when the host has swap", async () => {
  stubFetch({ [SUMMARY_PATH]: { body: { ...summary, swap: { totalBytes: 2147483648, freeBytes: 1073741824 } } } })
  renderWithProviders(<SummaryPage hostId="h1" />)
  await screen.findByRole("img", { name: "Swap: 1 of 2 GiB (50%)" })
})

test("shows the server's reason when the read fails", async () => {
  stubFetch({
    [SUMMARY_PATH]: {
      status: 409,
      body: { type: "about:blank", title: "Module unavailable", status: 409, detail: "the hostinfo module is unavailable on this host", code: "module.unavailable" },
    },
  })
  renderWithProviders(<SummaryPage hostId="h1" />)
  expect((await screen.findByText("the hostinfo module is unavailable on this host")).textContent).toBe(
    "the hostinfo module is unavailable on this host",
  )
})

test("the route bundle renders the summary at the module's index", async () => {
  stubFetch({ [SUMMARY_PATH]: { body: summary } })
  renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/hostinfo/*" element={<HostinfoRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/hostinfo" },
  )
  expect((await screen.findByText("web-1")).textContent).toBe("web-1")
})

test("the route bundle answers an unknown subpage", () => {
  stubFetch({})
  renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/hostinfo/*" element={<HostinfoRoutes hostId="h1" />} />
    </Routes>,
    { path: "/hosts/h1/hostinfo/nope" },
  )
  expect(screen.getByText("Page not found").textContent).toBe("Page not found")
})
