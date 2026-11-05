import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwExposure } from "./fixtures"
import { ExposurePage } from "./exposure-page"
import type { ExposureView } from "./types"

function epaRender(view: ExposureView) {
  stubFetch({ "GET /api/v1/hosts/h1/resources/firewall/exposure": { body: view } })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/exposure" element={<ExposurePage />} />
    </Routes>,
    { path: "/hosts/h1/firewall/exposure" },
  )
}

const epaClosed = {
  proto: "udp" as const,
  addr: "127.0.0.1",
  port: 5353,
  family: "v4" as const,
  process: "avahi-daemon",
  classes: [{ class: "internet" as const, verdict: "closed" as const, decidedBy: "filter/ufw-user-input#4" }],
}

test("the exposure table, its filters and its notes have no violations", async () => {
  const { container } = epaRender(fwExposure({ notes: ["IPv6 rules could not be read."] }))
  await screen.findByRole("table", { name: "Exposure" })
  await expectNoAxeViolations(container)
})

test("every verdict is a word as well as a colour", async () => {
  epaRender(fwExposure())
  const table = await screen.findByRole("table", { name: "Exposure" })
  const lists = screen.getAllByRole("list", { name: /^Reachability of/ })
  expect(lists).toHaveLength(2)
  expect(table.textContent).toContain("open")
  expect(table.textContent).toContain("partial")
})

test("the filters are used by keyboard: a radio group by arrow keys and the checkbox by Space", async () => {
  epaRender(fwExposure({ rows: [...fwExposure().rows, epaClosed] }))
  const user = userEvent.setup()
  const table = await screen.findByRole("table", { name: "Exposure" })
  expect(table.querySelectorAll("tbody tr")).toHaveLength(3)
  screen.getByRole("radio", { name: "All protocols" }).focus()
  await user.keyboard("{ArrowDown}")
  expect(screen.getByRole("radio", { name: "tcp" }).getAttribute("aria-checked")).toBe("true")
  expect(table.querySelectorAll("tbody tr")).toHaveLength(2)
  screen.getByRole("checkbox", { name: "Only ports the internet can reach" }).focus()
  expect(focusedName()).toBe("Only ports the internet can reach")
  await user.keyboard(" ")
  expect(screen.getByRole("checkbox", { name: "Only ports the internet can reach" }).getAttribute("aria-checked")).toBe("true")
})

test("a read that failed is announced as an alert", async () => {
  stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/exposure": {
      status: 409,
      body: { type: "about:blank", title: "Unavailable", status: 409, detail: "the firewall module is unavailable on this host", code: "module.unavailable" },
    },
  })
  renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/firewall/exposure" element={<ExposurePage />} />
    </Routes>,
    { path: "/hosts/h1/firewall/exposure" },
  )
  expect((await screen.findByRole("alert")).textContent).toBe("the firewall module is unavailable on this host")
})
