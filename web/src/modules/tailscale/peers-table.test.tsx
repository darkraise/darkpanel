import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders } from "@/test/utils"
import { PeersTable } from "./peers-table"
import type { TailscalePeer } from "./types"

function ptPeer(over: Partial<TailscalePeer> = {}): TailscalePeer {
  return {
    id: "n2",
    hostName: "db-1",
    dnsName: "db-1.tail1234.ts.net",
    os: "linux",
    addresses: ["100.64.0.2"],
    tags: [],
    online: true,
    connection: "direct",
    endpoint: "203.0.113.7:41641",
    region: "",
    exitNode: false,
    exitNodeOption: false,
    lastHandshake: "2026-10-31T08:00:00Z",
    ...over,
  }
}

const PT_PEERS: TailscalePeer[] = [
  ptPeer({ addresses: ["100.64.0.2", "fd7a:115c:a1e0::2"] }),
  ptPeer({ id: "n3", dnsName: "app-1.tail1234.ts.net", addresses: ["100.64.0.3"], connection: "derp", endpoint: "", region: "fra", lastHandshake: null }),
  ptPeer({ id: "n4", hostName: "edge", dnsName: "", addresses: ["100.64.0.4"], connection: "peer-relay", endpoint: "", exitNodeOption: true, lastHandshake: null }),
  ptPeer({ id: "n5", dnsName: "gw.tail1234.ts.net", addresses: ["100.64.0.5"], online: false, connection: "none", endpoint: "", exitNode: true, exitNodeOption: true, lastHandshake: null }),
]

function ptCells(row: HTMLElement | undefined): (string | null)[] {
  return within(row as HTMLElement).getAllByRole("cell").map((c) => c.textContent)
}

test("lists each peer with its addresses, state, connection, exit node role and last handshake", () => {
  renderWithProviders(<PeersTable peers={PT_PEERS} />)
  const table = screen.getByRole("table", { name: "Tailscale peers" })
  expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual([
    "Name",
    "Addresses",
    "Status",
    "Connection",
    "Exit node",
    "Last handshake",
  ])
  const rows = within(table).getAllByRole("row")
  expect(rows).toHaveLength(5)
  expect(ptCells(rows[1])).toEqual([
    "db-1.tail1234.ts.net",
    "100.64.0.2, fd7a:115c:a1e0::2",
    "Online",
    "direct 203.0.113.7:41641",
    "—",
    formatTime("2026-10-31T08:00:00Z"),
  ])
  expect(ptCells(rows[2])).toEqual(["app-1.tail1234.ts.net", "100.64.0.3", "Online", "derp fra", "—", "—"])
  expect(ptCells(rows[3])).toEqual(["edge", "100.64.0.4", "Online", "peer-relay", "Available", "—"])
  expect(ptCells(rows[4])).toEqual(["gw.tail1234.ts.net", "100.64.0.5", "Offline", "none", "In use", "—"])
})

test("no peers is an empty state, not an empty table", () => {
  renderWithProviders(<PeersTable peers={[]} />)
  expect(screen.getByText("No peers")).toBeTruthy()
  expect(screen.queryByRole("table")).toBeNull()
})

test("the peers table has no accessibility violations", async () => {
  const { container } = renderWithProviders(<PeersTable peers={PT_PEERS} />)
  expect(await axe(container)).toHaveNoViolations()
})
