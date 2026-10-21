import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import type { Host } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { HostsPage } from "./hosts"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"

const hosts: Host[] = [
  {
    id: "h1", name: "web-1", isLocal: true, online: true,
    modules: [
      { id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null },
      { id: "docker", version: 1, enabled: true, available: false, reason: "docker is not installed", checkedAt: null },
      { id: "ufw", version: 1, enabled: false, available: true, reason: null, checkedAt: null },
    ],
    ingestion: { ok: true, since: "2026-10-21T08:00:00Z", error: null },
  },
  { id: "h2", name: "db-1", isLocal: false, online: false, modules: [], ingestion: null },
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
    id: "h3", name: "cache-1", isLocal: false, online: true, modules: [],
    ingestion: { ok: false, since: "2026-10-21T08:05:00Z", error: "audit: ingest host h3: agent unreachable" },
  }
  stubFetch({ [HOSTS]: { body: { output: { hosts: [...hosts, failing] } } } })
  renderWithProviders(<HostsPage />)
  const row = (await screen.findByRole("link", { name: "cache-1" })).closest("tr")
  if (row === null) throw new Error("no row")
  within(row).getByText("Journal ingestion failing")
  within(row).getByText("audit: ingest host h3: agent unreachable")
  expect(screen.getAllByText("Journal ingestion failing")).toHaveLength(1)
})

test("says so when there are no hosts", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [] } } } })
  renderWithProviders(<HostsPage />)
  expect((await screen.findByText("No hosts yet")).textContent).toBe("No hosts yet")
})
