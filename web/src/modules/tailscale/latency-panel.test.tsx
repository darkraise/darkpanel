import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { LatencyPanel } from "./latency-panel"
import type { TailscaleLatency } from "./types"

const LP_LATENCY = "GET /api/v1/hosts/h1/resources/tailscale/latency"

const lpLatency: TailscaleLatency = {
  regions: [
    {
      regionId: 1,
      code: "nyc",
      name: "New York City",
      stun: { available: true, medianMs: 12.34, reason: "" },
      https: { available: true, medianMs: 20.5, reason: "" },
    },
    {
      regionId: 9,
      code: "fra",
      name: "Frankfurt",
      stun: { available: false, medianMs: 0, reason: "no STUN endpoint" },
      https: { available: true, medianMs: 101, reason: "" },
    },
  ],
}

test("shows both measurements for each region and says what they measure", async () => {
  stubFetch({ [LP_LATENCY]: { body: lpLatency } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  const table = await screen.findByRole("table", { name: "DERP latency" })
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Region", "STUN latency", "DERP HTTPS probe latency"])
  const rows = within(table).getAllByRole("row")
  const cells = (row: HTMLElement | undefined) => within(row as HTMLElement).getAllByRole("cell").map((c) => c.textContent)
  expect(cells(rows[1])).toEqual(["New York City (nyc)", "12.3 ms", "20.5 ms"])
  expect(cells(rows[2])).toEqual(["Frankfurt (fra)", "Unavailable: no STUN endpoint", "101.0 ms"])
  expect(screen.getByText(/measures reaching the server, not a relayed peer path/)).toBeTruthy()
})

test("an empty DERP map is an empty state", async () => {
  stubFetch({ [LP_LATENCY]: { body: { regions: [] } } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  expect(await screen.findByText("No DERP regions")).toBeTruthy()
  expect(screen.queryByRole("table")).toBeNull()
})

test("Measure again reads the latency once more", async () => {
  const calls = stubFetch({ [LP_LATENCY]: { body: lpLatency } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  await screen.findByRole("table", { name: "DERP latency" })
  expect(calls.filter((c) => c.path.endsWith("/latency"))).toHaveLength(1)
  await userEvent.setup().click(screen.getByRole("button", { name: "Measure again" }))
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("/latency"))).toHaveLength(2))
})

test("a failed read and an answer that is not a measurement are errors", async () => {
  stubFetch({
    [LP_LATENCY]: {
      status: 503,
      body: { type: "about:blank", title: "Unavailable", status: 503, detail: "tailscaled does not answer on its socket", code: "tailscale.unavailable" },
    },
  })
  const { unmount } = renderWithProviders(<LatencyPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("tailscaled does not answer on its socket")
  unmount()

  stubFetch({ [LP_LATENCY]: { body: { regions: "none" } } })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected latency measurement.")
})

test("a failed Measure again keeps the button so it can be retried", async () => {
  let reads = 0
  const calls = stubFetch({
    [LP_LATENCY]: () => {
      reads++
      if (reads === 2) {
        return {
          status: 503,
          body: { type: "about:blank", title: "Unavailable", status: 503, detail: "tailscaled does not answer on its socket", code: "tailscale.unavailable" },
        }
      }
      return { body: lpLatency }
    },
  })
  renderWithProviders(<LatencyPanel hostId="h1" />)
  await screen.findByRole("table", { name: "DERP latency" })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Measure again" }))
  expect((await screen.findByRole("alert")).textContent).toBe("tailscaled does not answer on its socket")
  await user.click(await screen.findByRole("button", { name: "Measure again" }))
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("/latency"))).toHaveLength(3))
  expect(await screen.findByRole("table", { name: "DERP latency" })).toBeTruthy()
})

test("the latency panel has no accessibility violations", async () => {
  stubFetch({ [LP_LATENCY]: { body: lpLatency } })
  const { container } = renderWithProviders(<LatencyPanel hostId="h1" />)
  await screen.findByRole("table", { name: "DERP latency" })
  expect(await axe(container)).toHaveNoViolations()
})
