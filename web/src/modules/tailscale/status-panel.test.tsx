import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { StatusPanel } from "./status-panel"
import type { TailscaleStatus } from "./types"

const SP_STATUS = "GET /api/v1/hosts/h1/resources/tailscale/status"

function spStatus(over: Partial<TailscaleStatus> = {}): TailscaleStatus {
  return {
    version: "1.104.1",
    backendState: "Running",
    self: {
      id: "n1",
      hostName: "web-1",
      dnsName: "web-1.tail1234.ts.net",
      os: "linux",
      addresses: ["100.64.0.1", "fd7a:115c:a1e0::1"],
      tags: ["tag:server"],
    },
    tailnet: "example.com",
    magicDNSSuffix: "tail1234.ts.net",
    preferredDerp: "fra",
    runSSH: true,
    peers: [],
    health: [],
    ...over,
  }
}

function spValue(list: HTMLElement, label: string): string | null | undefined {
  return within(list).getByText(label, { selector: "dt" }).nextElementSibling?.textContent
}

test("shows the node's state, names, addresses, home DERP region and SSH setting", async () => {
  stubFetch({ [SP_STATUS]: { body: spStatus() } })
  renderWithProviders(<StatusPanel hostId="h1" />)
  const list = await screen.findByLabelText("Tailscale status")
  expect(spValue(list, "Backend state")).toBe("Running")
  expect(spValue(list, "Version")).toBe("1.104.1")
  expect(spValue(list, "Tailnet")).toBe("example.com")
  expect(spValue(list, "MagicDNS suffix")).toBe("tail1234.ts.net")
  expect(spValue(list, "DNS name")).toBe("web-1.tail1234.ts.net")
  expect(spValue(list, "Addresses")).toBe("100.64.0.1, fd7a:115c:a1e0::1")
  expect(spValue(list, "Tags")).toBe("tag:server")
  expect(spValue(list, "Home DERP region")).toBe("fra")
  expect(spValue(list, "Tailscale SSH")).toBe("On")
})

test("a logged-out node shows its state and dashes where it has no value", async () => {
  stubFetch({
    [SP_STATUS]: { body: spStatus({ backendState: "NeedsLogin", self: null, tailnet: "", magicDNSSuffix: "", preferredDerp: "", runSSH: false }) },
  })
  renderWithProviders(<StatusPanel hostId="h1" />)
  const list = await screen.findByLabelText("Tailscale status")
  expect(spValue(list, "Backend state")).toBe("NeedsLogin")
  for (const label of ["Tailnet", "MagicDNS suffix", "DNS name", "Addresses", "Tags", "Home DERP region"]) {
    expect(spValue(list, label)).toBe("—")
  }
  expect(spValue(list, "Tailscale SSH")).toBe("Off")
})

test("health warnings are listed, and none shows no list", async () => {
  stubFetch({ [SP_STATUS]: { body: spStatus({ health: ["Tailscale can't reach the log server.", "The DNS configuration is broken."] }) } })
  const { unmount } = renderWithProviders(<StatusPanel hostId="h1" />)
  const warnings = await screen.findByRole("list", { name: "Health warnings" })
  expect(within(warnings).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "Tailscale can't reach the log server.",
    "The DNS configuration is broken.",
  ])
  unmount()

  stubFetch({ [SP_STATUS]: { body: spStatus() } })
  renderWithProviders(<StatusPanel hostId="h1" />)
  await screen.findByLabelText("Tailscale status")
  expect(screen.queryByRole("list", { name: "Health warnings" })).toBeNull()
})

test("an unavailable tailscaled is an error with the host's own reason", async () => {
  stubFetch({
    [SP_STATUS]: {
      status: 503,
      body: { type: "about:blank", title: "Unavailable", status: 503, detail: "tailscaled does not answer on its socket", code: "tailscale.unavailable" },
    },
  })
  renderWithProviders(<StatusPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("tailscaled does not answer on its socket")
})

test("an answer that is not a status is an error", async () => {
  stubFetch({ [SP_STATUS]: { body: { backendState: 7 } } })
  renderWithProviders(<StatusPanel hostId="h1" />)
  expect((await screen.findByRole("alert")).textContent).toBe("The host answered with an unexpected Tailscale status.")
})

test("the status panel has no accessibility violations", async () => {
  stubFetch({ [SP_STATUS]: { body: spStatus({ health: ["The DNS configuration is broken."] }) } })
  const { container } = renderWithProviders(<StatusPanel hostId="h1" />)
  await screen.findByLabelText("Tailscale status")
  expect(await axe(container)).toHaveNoViolations()
})
