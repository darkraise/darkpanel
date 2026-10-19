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

function valueOf(label: string): string | null | undefined {
  return screen.getByText(label, { selector: "dt" }).nextElementSibling?.textContent
}

test("shows the summary as labelled, human-readable values", async () => {
  stubFetch({ [SUMMARY_PATH]: { body: summary } })
  renderWithProviders(<SummaryPage hostId="h1" />)
  await screen.findByText("Hostname", { selector: "dt" })
  expect(valueOf("Hostname")).toBe("web-1")
  expect(valueOf("Operating system")).toBe("Ubuntu 24.04.3 LTS")
  expect(valueOf("Kernel")).toBe("6.8.0-45-generic")
  expect(valueOf("Architecture")).toBe("amd64")
  expect(valueOf("Uptime")).toBe("3 d 4 h 5 min")
  expect(valueOf("Booted")).toBe(formatTime("2026-10-16T03:55:00Z"))
  expect(valueOf("CPU")).toBe("AMD EPYC 7763 (4 logical CPUs)")
  expect(valueOf("Load average")).toBe("0.50 / 0.25 / 0.10")
  expect(valueOf("Memory")).toBe("8.0 GiB available of 16.0 GiB")
  expect(valueOf("Swap")).toBe("None")
  expect(valueOf("Root filesystem")).toBe("50.0 GiB available of 100 GiB")
  expect(valueOf("Darkpanel version")).toBe("0.1.0")
})

test("shows swap use when the host has swap", async () => {
  stubFetch({ [SUMMARY_PATH]: { body: { ...summary, swap: { totalBytes: 2147483648, freeBytes: 1073741824 } } } })
  renderWithProviders(<SummaryPage hostId="h1" />)
  await screen.findByText("Swap", { selector: "dt" })
  expect(valueOf("Swap")).toBe("1.0 GiB free of 2.0 GiB")
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
