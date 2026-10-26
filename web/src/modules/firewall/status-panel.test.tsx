import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { fwStatus } from "./fixtures"
import { StatusBanners, StatusSummary } from "./status-panel"

function spValue(list: HTMLElement, label: string): string | null | undefined {
  return within(list).getByText(label, { selector: "dt" }).nextElementSibling?.textContent
}

test("shows the configured and the loaded state side by side", () => {
  renderWithProviders(<StatusSummary status={fwStatus()} />)
  const configured = screen.getByLabelText("Configured state")
  expect(spValue(configured, "Enabled at boot")).toBe("Yes")
  expect(spValue(configured, "Incoming")).toBe("deny")
  expect(spValue(configured, "Outgoing")).toBe("allow")
  expect(spValue(configured, "Routed")).toBe("deny")
  expect(spValue(configured, "Logging")).toBe("low")
  expect(spValue(configured, "IPv6")).toBe("Yes")
  const loaded = screen.getByLabelText("Loaded state")
  expect(spValue(loaded, "iptables variant")).toBe("nft")
  expect(spValue(loaded, "IPv6 rules")).toBe("Read")
  expect(spValue(loaded, "Chains loaded")).toBe("2")
  expect(spValue(loaded, "In sync")).toBe("Yes")
  expect(spValue(loaded, "net/ipv4/ip_forward")).toBe("0")
  expect(spValue(loaded, "net/ipv4/conf/all/rp_filter")).toBe("2")
  expect(screen.getByText("Active").textContent).toBe("Active")
})

test("an inactive firewall says so", () => {
  renderWithProviders(<StatusSummary status={fwStatus({ active: false })} />)
  expect(screen.getByText("Inactive").textContent).toBe("Inactive")
})

test("a supported host in sync shows no banner", () => {
  stubFetch({})
  renderWithProviders(<StatusBanners hostId="h1" status={fwStatus()} />)
  expect(screen.queryByRole("alert")).toBeNull()
  expect(screen.queryByText("Out of sync")).toBeNull()
})

test("an unsupported configuration is a banner naming every reason", () => {
  stubFetch({})
  renderWithProviders(
    <StatusBanners hostId="h1" status={fwStatus({ supported: false, inSync: false, unsupported: ["/etc/ufw/before.init is executable", "MANAGE_BUILTINS=yes"] })} />,
  )
  const alert = screen.getByRole("alert")
  expect(within(alert).getByText("Unsupported configuration").textContent).toBe("Unsupported configuration")
  expect(within(within(alert).getByRole("list", { name: "Unsupported configuration reasons" })).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "/etc/ufw/before.init is executable",
    "MANAGE_BUILTINS=yes",
  ])
  expect(screen.queryByText("Out of sync")).toBeNull()
})

test("an out-of-sync host names the differing chains and offers a repair", async () => {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.state.repair", available: true, reason: null }] },
    },
    "POST /api/v1/hosts/h1/actions/firewall.state.repair/v1/plan": {
      body: { planId: "p1", hostId: "h1", action: "firewall.state.repair", version: 1, inputHash: "h", diff: [{ op: "+", text: "load ufw-user-input" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  renderWithProviders(
    <StatusBanners
      hostId="h1"
      status={fwStatus({
        inSync: false,
        outOfSync: [
          { family: "v4", table: "filter", chain: "ufw-user-input", onlyLoaded: ["-A ufw-user-input -s 192.0.2.98/32 -j ACCEPT"] },
          { family: "v6", table: "filter", chain: "ufw6-user-input", orderDiffers: true },
        ],
      })}
    />,
  )
  const banner = screen.getByText("Out of sync").closest<HTMLElement>("[role=status]")
  if (banner === null) throw new Error("the out-of-sync banner has no status role")
  expect(within(within(banner).getByRole("list", { name: "Differing chains" })).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "v4 filter ufw-user-input",
    "v6 filter ufw6-user-input",
  ])
  await userEvent.setup().click(within(banner).getByRole("button", { name: "Repair" }))
  const dialog = await screen.findByRole("dialog")
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ load ufw-user-input")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: {} })
  expect(within(dialog).getByLabelText("Revert after (seconds)")).toBeTruthy()
})

test("a sync that could not be checked says why", () => {
  stubFetch({})
  renderWithProviders(<StatusBanners hostId="h1" status={fwStatus({ inSync: false, syncError: "firewall: predict: no ufw-user-input chain" })} />)
  expect(screen.getByText(/could not be checked/).textContent).toBe(
    "Whether the loaded rules match UFW's files could not be checked, so every change except a repair is refused: firewall: predict: no ufw-user-input chain",
  )
  expect(screen.queryByRole("list", { name: "Differing chains" })).toBeNull()
})

test("the banners and the summary have no accessibility violations", async () => {
  stubFetch({})
  const { container } = renderWithProviders(
    <>
      <StatusBanners hostId="h1" status={fwStatus({ inSync: false, outOfSync: [{ family: "v4", table: "filter", chain: "ufw-user-input" }] })} />
      <StatusSummary status={fwStatus()} />
    </>,
  )
  expect(await axe(container)).toHaveNoViolations()
})
