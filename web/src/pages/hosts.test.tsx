import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import type { Host } from "@/api/types"
import { formatTime } from "@/lib/format"
import { makeHost, renderWithProviders, stubFetch } from "@/test/utils"
import { HostsPage } from "./hosts"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"

const hosts: Host[] = [
  {
    id: "h1", name: "web-1", isLocal: true, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true,
    modules: [
      { id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null },
      { id: "docker", version: 1, enabled: true, available: false, reason: "docker is not installed", checkedAt: null },
      { id: "ufw", version: 1, enabled: false, available: true, reason: null, checkedAt: null },
    ],
    ingestion: { ok: true, since: "2026-10-21T08:00:00Z", error: null },
  },
  { id: "h2", name: "db-1", isLocal: false, state: "enrolled", online: false, lastSeenAt: null, agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [], ingestion: null },
]

test("lists hosts with the local marker and module states", async () => {
  const calls = stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  renderWithProviders(<HostsPage />)
  const link = await screen.findByRole("link", { name: "web-1" })
  expect(link.getAttribute("href")).toBe("/hosts/h1")
  const row = link.closest("tr")
  if (row === null) throw new Error("no row")
  const cells = within(row)
  expect(cells.getByText("This controller").textContent).toBe("This controller")
  expect(cells.getByText("Online").textContent).toBe("Online")
  expect(cells.getByText("hostinfo: on").textContent).toBe("hostinfo: on")
  expect(cells.getByText("docker: unavailable").textContent).toBe("docker: unavailable")
  expect(cells.getByText("ufw: off").textContent).toBe("ufw: off")
  expect(cells.getByText("docker: docker is not installed").textContent).toBe("docker: docker is not installed")

  const other = screen.getByRole("link", { name: "db-1" }).closest("tr")
  if (other === null) throw new Error("no row")
  expect(within(other).getByText("Offline").textContent).toBe("Offline")
  expect(within(other).queryByText("This controller")).toBeNull()
  expect(calls[0]?.body).toEqual({ input: {} })
})

test("marks only a host whose journal ingestion fails, with the error beside it", async () => {
  const failing: Host = {
    id: "h3", name: "cache-1", isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [],
    ingestion: { ok: false, since: "2026-10-21T08:05:00Z", error: "agent unreachable" },
  }
  stubFetch({ [HOSTS]: { body: { output: { hosts: [...hosts, failing] } } } })
  renderWithProviders(<HostsPage />)
  const row = (await screen.findByRole("link", { name: "cache-1" })).closest("tr")
  if (row === null) throw new Error("no row")
  within(row).getByText("Journal ingestion failing")
  within(row).getByText("agent unreachable")
  expect(screen.getAllByText("Journal ingestion failing")).toHaveLength(1)
})

test("says so when there are no hosts", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [] } } } })
  renderWithProviders(<HostsPage />)
  expect((await screen.findByText("No hosts yet")).textContent).toBe("No hosts yet")
})

test("UI-1: shows last seen, versions, platform and the enrollment state", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [
    makeHost({ id: "a", name: "web-1", lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.2.0", protocolVersion: 3, os: "debian-12", arch: "arm64" }),
    makeHost({ id: "b", name: "new-1", state: "pending", online: false, lastSeenAt: null, agentVersion: null, protocolVersion: null, os: null, arch: null }),
    makeHost({ id: "c", name: "old-1", state: "revoked", online: false, supported: false, agentVersion: "0.9.0" }),
  ] } } } })
  renderWithProviders(<HostsPage />)
  const row = async (name: string) => {
    const tr = (await screen.findByRole("link", { name })).closest("tr")
    if (tr === null) throw new Error("no row")
    return within(tr)
  }
  const web = await row("web-1")
  web.getByText(`Last seen ${formatTime("2026-11-11T08:00:00Z")}`)
  web.getByText("1.2.0")
  web.getByText("protocol 3")
  web.getByText("debian-12 / arm64")
  expect(web.queryByText("Pending")).toBeNull()
  const fresh = await row("new-1")
  fresh.getByText("Pending")
  fresh.getByText("Never seen")
  expect(fresh.getAllByText("—")).toHaveLength(3)
  const old = await row("old-1")
  old.getByText("Revoked")
  old.getByText("Unsupported")
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Status", "Version", "Platform", "Modules"])
})

test("UX-1: a worst-case name and version wrap inside their cells", async () => {
  const name = `web-${"x".repeat(59)}`
  const version = `1.0.0-beta.1+${"a".repeat(27)}`
  stubFetch({ [HOSTS]: { body: { output: { hosts: [makeHost({ name, agentVersion: version })] } } } })
  renderWithProviders(<HostsPage />)
  const link = await screen.findByRole("link", { name })
  expect(link.className).toContain("break-all")
  expect(screen.getByText(version).closest(".break-all")).not.toBeNull()
})

test("UX-12: the table scrolls inside its own container", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [makeHost()] } } } })
  renderWithProviders(<HostsPage />)
  const table = await screen.findByRole("table")
  expect(table.closest(".overflow-x-auto")).not.toBeNull()
})
