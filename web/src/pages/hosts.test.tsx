import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { Host } from "@/api/types"
import { makeHost, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { HostsPage } from "./hosts"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const TABLE = { path: "/?view=table" }

const hosts: Host[] = [
  {
    id: "h1", name: "web-1", scope: "admin", isLocal: true, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true,
    modules: [
      { id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null },
      { id: "docker", version: 1, enabled: true, available: false, reason: "docker is not installed", checkedAt: null },
      { id: "ufw", version: 1, enabled: false, available: true, reason: null, checkedAt: null },
    ],
    ingestion: { ok: true, since: "2026-10-21T08:00:00Z", error: null },
  },
  { id: "h2", name: "db-1", scope: "admin", isLocal: false, state: "enrolled", online: false, lastSeenAt: null, agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [], ingestion: null },
]

test("lists hosts with the local marker and module icons", async () => {
  const calls = stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  renderWithProviders(<HostsPage />, TABLE)
  const link = await screen.findByRole("link", { name: "web-1" })
  expect(link.getAttribute("href")).toBe("/hosts/h1")
  const row = link.closest("tr")
  if (row === null) throw new Error("no row")
  const cells = within(row)
  expect(cells.getByText("This controller").textContent).toBe("This controller")
  expect(cells.getByText("Online").textContent).toBe("Online")
  const modules = within(cells.getByRole("list", { name: "Modules" }))
  modules.getByText("Host information")
  expect(modules.getByText("Docker (unavailable): docker is not installed").closest("li")?.getAttribute("title")).toBe("Docker (unavailable): docker is not installed")
  expect(modules.queryByText(/ufw/i)).toBeNull()

  const other = screen.getByRole("link", { name: "db-1" }).closest("tr")
  if (other === null) throw new Error("no row")
  expect(within(other).getByText("Offline").textContent).toBe("Offline")
  within(other).getByText("Never seen")
  expect(within(other).queryByText("This controller")).toBeNull()
  expect(calls[0]?.body).toEqual({ input: {} })
})

test("marks only a host whose journal ingestion fails, with the error beside it", async () => {
  const failing: Host = {
    id: "h3", name: "cache-1", scope: "admin", isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [],
    ingestion: { ok: false, since: "2026-10-21T08:05:00Z", error: "agent unreachable" },
  }
  stubFetch({ [HOSTS]: { body: { output: { hosts: [...hosts, failing] } } } })
  renderWithProviders(<HostsPage />, TABLE)
  const row = (await screen.findByRole("link", { name: "cache-1" })).closest("tr")
  if (row === null) throw new Error("no row")
  within(row).getByText("Journal ingestion failing")
  await userEvent.click(within(row).getByRole("button", { name: "Why journal ingestion fails" }))
  expect((await screen.findAllByText("agent unreachable")).length).toBeGreaterThan(0)
  expect(screen.getAllByText("Journal ingestion failing")).toHaveLength(1)
})

test("a failed host list read offers Retry, which reads the list again", async () => {
  let calls = 0
  stubFetch({ [HOSTS]: () => (++calls === 1 ? { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "store.busy" } } : { body: { output: { hosts } } }) })
  renderWithProviders(<HostsPage />)
  await screen.findByText("the database is busy")
  await userEvent.click(screen.getByRole("button", { name: "Retry" }))
  await screen.findByRole("article", { name: "web-1" })
  expect(calls).toBe(2)
})

test("says so when there are no hosts", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [] } } } })
  renderWithProviders(<HostsPage />)
  expect((await screen.findByText("No hosts yet")).textContent).toBe("No hosts yet")
})

test("UI-3: the table shows the platform, the enrollment state and memory as on the board", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [
    makeHost({ id: "a", name: "web-1", lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.2.0", protocolVersion: 3, os: "debian-12", arch: "arm64" }),
    makeHost({ id: "b", name: "new-1", state: "pending", online: false, lastSeenAt: null, agentVersion: null, protocolVersion: null, os: null, arch: null }),
    makeHost({ id: "c", name: "old-1", state: "revoked", online: false, supported: false, agentVersion: "0.9.0" }),
  ] } } } })
  renderWithProviders(<HostsPage />, TABLE)
  const row = async (name: string) => {
    const tr = (await screen.findByRole("link", { name })).closest("tr")
    if (tr === null) throw new Error("no row")
    return within(tr)
  }
  const web = await row("web-1")
  web.getByText("debian-12 · arm64")
  expect(web.queryByText("Pending")).toBeNull()
  const fresh = await row("new-1")
  fresh.getByText("Pending")
  expect(fresh.getAllByText("—")).toHaveLength(2)
  const old = await row("old-1")
  old.getByText("Revoked")
  old.getByText("Unsupported")
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Host", "Status", "System", "Memory", "Modules"])
})

test("UX-9: a worst-case name is truncated in its cell with the full name in its title", async () => {
  const name = `web-${"x".repeat(59)}`
  stubFetch({ [HOSTS]: { body: { output: { hosts: [makeHost({ name })] } } } })
  renderWithProviders(<HostsPage />, TABLE)
  const link = await screen.findByRole("link", { name })
  expect(link.className).toContain("truncate")
  expect(link.getAttribute("title")).toBe(name)
})

test("UX-12: the table scrolls inside its own container", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [makeHost()] } } } })
  renderWithProviders(<HostsPage />, TABLE)
  const table = await screen.findByRole("table")
  expect(table.closest(".overflow-x-auto")).not.toBeNull()
})

const GiB = 2 ** 30
const SUMMARY = (id: string) => `GET /api/v1/hosts/${id}/resources/hostinfo/summary`

test("A11: a card per host with online state, memory and disk bars and module icons; the table toggle lives in the URL", async () => {
  stubFetch({
    "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [makeHost({ id: "h1", name: "web-1", modules: [{ id: "docker", version: 1, enabled: true, available: false, reason: null, checkedAt: null }] }), makeHost({ id: "h2", name: "db-1", online: false, lastSeenAt: "2026-11-19T10:00:00Z" })] } } },
    "GET /api/v1/hosts/h1/resources/hostinfo/summary": { body: { cpu: { model: "QEMU", logicalCount: 4 }, memory: { totalBytes: 8 * GiB, availableBytes: 2 * GiB }, rootFs: { totalBytes: 100 * GiB, availableBytes: 59 * GiB }, swap: { totalBytes: 0, freeBytes: 0 }, load: { one: 1.2, five: 1, fifteen: 0.8 }, uptimeSeconds: 90000, bootTime: "2026-11-18T11:00:00Z" } },
  })
  renderWithProviders(<HostsPage />, { path: "/" })
  const card = within(await screen.findByRole("article", { name: "web-1" }))
  await card.findByRole("img", { name: /^Memory: 6 of 8/ })
  card.getByRole("img", { name: /^Disk: 41 of 100/ })
  card.getByText("Docker (unavailable)")
  within(screen.getByRole("article", { name: "db-1" })).getByText(/Last seen/)
  await userEvent.click(screen.getByRole("radio", { name: "Table" }))
  expect(screen.getByTestId("location").textContent).toContain("view=table")
})

test("UX-9: cards carry the host's state chips, link to the host and read no summary while offline", async () => {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts: [
      makeHost({ id: "h1", name: "web-1", scope: "admin", isLocal: true, os: "ubuntu-24.04", arch: "arm64", ingestion: { ok: false, since: "2026-10-21T08:05:00Z", error: "agent unreachable" } }),
      makeHost({ id: "h2", name: "new-1", state: "pending", online: false, lastSeenAt: null, os: null, arch: null }),
      makeHost({ id: "h3", name: "old-1", state: "revoked", online: false, supported: false, agentVersion: "0.9.0" }),
    ] } } },
    [SUMMARY("h1")]: { body: { memory: { totalBytes: 8 * GiB, availableBytes: 7.5 * GiB }, rootFs: { totalBytes: 100 * GiB, availableBytes: 99 * GiB } } },
  })
  renderWithProviders(<HostsPage />)
  const web = within(await screen.findByRole("article", { name: "web-1" }))
  web.getByText("Online")
  web.getByText("This controller")
  web.getByText("ubuntu-24.04")
  web.getByText("arm64")
  web.getByText("Journal ingestion failing")
  web.getByRole("button", { name: "Why journal ingestion fails" })
  expect(web.getByRole("link", { name: "Open web-1" }).getAttribute("href")).toBe("/hosts/h1")
  await web.findByRole("img", { name: /^Memory: 0\.5 of 8 GiB/ })
  const fresh = within(screen.getByRole("article", { name: "new-1" }))
  fresh.getByText("Pending")
  fresh.getByText("Never seen")
  const old = within(screen.getByRole("article", { name: "old-1" }))
  old.getByText("Revoked")
  old.getByText("Unsupported")
  expect(calls.filter((c) => c.path.endsWith("/hostinfo/summary")).map((c) => c.path)).toEqual(["/api/v1/hosts/h1/resources/hostinfo/summary"])
})

test("UX-9: a failed summary read shows the error icon with the reason in a tip, and a 64-character name wraps", async () => {
  const name = `web-${"x".repeat(60)}`
  stubFetch({
    [HOSTS]: { body: { output: { hosts: [makeHost({ name })] } } },
    [SUMMARY("h1")]: { status: 502, body: { type: "about:blank", title: "Bad gateway", status: 502, detail: "the agent did not answer", code: "agent.unreachable" } },
  })
  renderWithProviders(<HostsPage />)
  const card = within(await screen.findByRole("article", { name }))
  await card.findByRole("img", { name: "Host information unavailable" })
  card.getByText("Usage unavailable")
  await userEvent.click(card.getByRole("button", { name: "Why unavailable" }))
  expect((await screen.findAllByText("the agent did not answer")).length).toBeGreaterThan(0)
  expect(card.getByText(name).className).toContain("break-all")
  expect(card.queryByRole("img", { name: /^Memory/ })).toBeNull()
})

test("A51: 50 slow hosts render frames at once, keep 6 reads in flight, and time out to an error icon at 5 s", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let inFlight = 0, peak = 0
  const items = Array.from({ length: 50 }, (_, i) => makeHost({ id: `h${i}`, name: `host-${i}` }))
  const routes: Record<string, StubHandler> = { "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: items } } } }
  for (const h of items) routes[`GET /api/v1/hosts/${h.id}/resources/hostinfo/summary`] = async () => { inFlight++; peak = Math.max(peak, inFlight); await new Promise((r) => setTimeout(r, 8000)); inFlight--; return { body: {} } }
  stubFetch(routes)
  renderWithProviders(<HostsPage />, { path: "/" })
  expect(await screen.findAllByRole("article")).toHaveLength(50)
  await vi.advanceTimersByTimeAsync(4900)
  expect(peak).toBeLessThanOrEqual(6)
  await vi.advanceTimersByTimeAsync(200)
  expect(screen.getAllByRole("img", { name: /Host information unavailable/ }).length).toBeGreaterThanOrEqual(6)
  vi.useRealTimers()
})
