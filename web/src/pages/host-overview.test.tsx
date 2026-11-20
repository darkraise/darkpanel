import { act, screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { ModuleState } from "@/api/types"
import { formatTime } from "@/lib/format"
import { statusQueryKey } from "@/modules/firewall/types"
import { makeHost, renderWithProviders, stubFetch } from "@/test/utils"
import { HostOverview } from "./host-overview"

const STATUS = "POST /api/v1/hosts/h1/actions/core.update.status/v1/read"
const tx = {
  id: "t1", operationId: "t1", parentId: null, kind: "update", fromVersion: "1.0.0", toVersion: "1.1.0", targetVersion: "1.1.0",
  state: "rolled-back", error: { code: "update.health_failed", message: "the health check failed" },
  startedAt: "2026-11-11T07:00:00Z", updatedAt: "2026-11-11T07:05:00Z",
}

test("UI-4: the Agent tile carries the agent's version, protocol and its update actions", async () => {
  stubFetch({ [STATUS]: { body: { output: { transactions: [] } } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1", agentVersion: "1.2.0", protocolVersion: 3, os: "ubuntu-24.04", arch: "amd64" })} />)
  expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Overview", "Agent update history", "Enrollment and removal"])
  const agent = within(screen.getByRole("region", { name: "Agent" }))
  agent.getByText("1.2.0")
  agent.getByText("Protocol 3")
  expect(agent.getByRole("button", { name: "Update agent" }).hasAttribute("disabled")).toBe(false)
  agent.getByRole("button", { name: "Change policy" })
  expect(screen.queryByText(/Last seen/)).toBeNull()
})

test("UI-4: a pending host says its agent has not reported and reads nothing", () => {
  const calls = stubFetch({})
  renderWithProviders(<HostOverview host={makeHost({ state: "pending", online: false, lastSeenAt: null, agentVersion: null, protocolVersion: null, os: null, arch: null })} />)
  within(screen.getByRole("region", { name: "Agent" })).getByText("Not reported")
  within(screen.getByRole("region", { name: "Updates" })).getByText("Never seen")
  expect(screen.queryByText("Agent update needed")).toBeNull()
  expect(calls).toHaveLength(0)
})

test("UI-2: the controller's own host says why it has no removal controls", () => {
  stubFetch({ [STATUS]: { body: { output: { transactions: [] } } } })
  renderWithProviders(<HostOverview host={makeHost({ isLocal: true })} />)
  const card = within(screen.getByRole("region", { name: "Enrollment and removal" }))
  card.getByText("The controller's own host cannot be revoked, deleted or given a new enrollment token.")
  expect(card.queryAllByRole("button")).toHaveLength(0)
})

test("UX-2: the update history lists transactions newest first with state and error", async () => {
  const calls = stubFetch({ [STATUS]: { body: { output: { transactions: [tx] } } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  const table = await screen.findByRole("table", { name: "Update history" })
  const row = within(table).getAllByRole("row")[1]
  if (row === undefined) throw new Error("no row")
  within(row).getByText("1.0.0 → 1.1.0")
  within(row).getByText("rolled-back")
  within(row).getByText("the health check failed")
  expect(calls.find((c) => c.path.endsWith("/read"))?.body).toEqual({ input: {} })
})

test("UX-2: the narrow update history lists each transaction with its state, versions, time, kind and error", async () => {
  const committed = { ...tx, id: "t2", operationId: "t2", state: "committed", error: null, startedAt: "2026-11-10T07:00:00Z" }
  stubFetch({ [STATUS]: { body: { output: { transactions: [tx, committed] } } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  const list = await screen.findByRole("list", { name: "Update history" })
  const items = within(list).getAllByRole("listitem")
  expect(items).toHaveLength(2)
  const [first, second] = items
  if (first === undefined || second === undefined) throw new Error("no items")
  within(first).getByText("rolled-back")
  within(first).getByText("1.0.0 → 1.1.0")
  within(first).getByText(`${formatTime("2026-11-11T07:00:00Z")} · update`)
  within(first).getByText("Error: the health check failed")
  within(second).getByText("committed")
  within(second).getByText(`${formatTime("2026-11-10T07:00:00Z")} · update`)
  expect(within(second).queryByText(/^Error:/)).toBeNull()
})

test("UI-4: each history badge carries the variant of its transaction state", async () => {
  const states: [string, string][] = [["pending", "secondary"], ["rolled-back", "destructive"], ["committed", "default"]]
  const transactions = states.map(([state], i) => ({ ...tx, id: `t${i}`, operationId: `t${i}`, state }))
  stubFetch({ [STATUS]: { body: { output: { transactions } } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  const table = await screen.findByRole("table", { name: "Update history" })
  for (const [state, variant] of states) expect(within(table).getByText(state).closest("[data-variant]")?.getAttribute("data-variant")).toBe(variant)
  const list = screen.getByRole("list", { name: "Update history" })
  for (const [state, variant] of states) expect(within(list).getByText(state).closest("[data-variant]")?.getAttribute("data-variant")).toBe(variant)
})

test("UX-2: an empty history says so", async () => {
  stubFetch({ [STATUS]: { body: { output: { transactions: [] } } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  await screen.findByText("No updates yet")
})

test("UX-2: an offline host's history is not read", async () => {
  const calls = stubFetch({})
  renderWithProviders(<HostOverview host={makeHost({ id: "h1", online: false })} />)
  screen.getByText("The update history is unavailable while the host is offline.")
  expect(calls).toHaveLength(0)
})

test("UX-2: a failed history read shows its message in the card", async () => {
  stubFetch({ [STATUS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "no updater on this host", code: "unavailable" } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  expect((await screen.findByRole("alert")).textContent).toContain("no updater on this host")
})

const GiB = 2 ** 30

test("A12: gauges, load per core, uptime and the four service tiles", async () => {
  stubFetch({
    "GET /api/v1/hosts/h1/resources/hostinfo/summary": { body: { cpu: { model: "QEMU", logicalCount: 4 }, memory: { totalBytes: 8 * GiB, availableBytes: 2 * GiB }, swap: { totalBytes: 2 * GiB, freeBytes: 2 * GiB }, rootFs: { totalBytes: 100 * GiB, availableBytes: 59 * GiB }, load: { one: 1.2, five: 1, fifteen: 0.8 }, uptimeSeconds: 90000, bootTime: "2026-11-18T11:00:00Z" } },
    "GET /api/v1/hosts/h1/resources/system/updates": { body: { upgradable: [{ name: "a", from: "1", to: "2", origin: "x-security", security: true }], removals: [], heldBack: [], refreshedAt: null, failedSources: [], dpkgInterrupted: false } },
    "GET /api/v1/hosts/h1/resources/system/reboot": { body: { required: true, packages: [], kernel: null, services: [] } },
  })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1", modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })} />)
  await screen.findByRole("img", { name: /^Memory: 6 of 8/ })
  screen.getByRole("img", { name: /^Disk: 41 of 100/ })
  screen.getByRole("img", { name: /^Swap: 0 of 2/ })
  screen.getByRole("group", { name: "Load per core: 0.30" })
  screen.getByRole("group", { name: "Uptime: 1 d 1 h" })
  for (const name of ["Updates", "Docker", "Firewall", "Tailscale"]) screen.getByRole("region", { name })
  within(screen.getByRole("region", { name: "Updates" })).getByText("Reboot needed")
})

test("UX-9: an offline host keeps the tile grid with last seen in place of each gauge and its agent actions disabled", () => {
  stubFetch({})
  renderWithProviders(<HostOverview host={makeHost({ id: "h1", online: false, lastSeenAt: "2026-11-19T10:00:00Z" })} />)
  expect(screen.queryByRole("img", { name: /^Memory/ })).toBeNull()
  for (const name of ["Memory", "Disk", "Swap"]) within(screen.getByRole("region", { name })).getByText(`Last seen ${formatTime("2026-11-19T10:00:00Z")}`)
  screen.getByRole("group", { name: "Load per core: —" })
  screen.getByRole("group", { name: "Uptime: —" })
  expect(within(screen.getByRole("region", { name: "Agent" })).getByRole("button", { name: "Update agent" }).hasAttribute("disabled")).toBe(true)
})

test("UX-9: offline, firewall and Tailscale values read earlier stay under Last known and Unknown pills", async () => {
  const calls = stubFetch({})
  const { client } = renderWithProviders(<HostOverview host={makeHost({ id: "h1", online: false, modules: [on("firewall"), on("tailscale")] })} />)
  within(screen.getByRole("region", { name: "Firewall" })).getByText(/Last seen/)
  act(() => {
    client.setQueryData(statusQueryKey("h1"), { supported: true, unsupported: [], active: true, inSync: true, outOfSync: [], configured: {}, loaded: {}, rules: [{}, {}, {}] })
    client.setQueryData(["resource", "h1", "tailscale.status"], { version: "1.80.0", backendState: "Running", self: { id: "n1", hostName: "web-1", dnsName: "web-1.ts.net.", os: "linux", addresses: ["100.64.0.2"], tags: [] }, tailnet: "t", magicDNSSuffix: "ts.net", preferredDerp: "fra", runSSH: false, peers: [], health: [] })
  })
  const firewall = within(screen.getByRole("region", { name: "Firewall" }))
  await firewall.findByText("Last known")
  expect(screen.getByRole("region", { name: "Firewall" }).textContent).toContain("3 rules")
  const tailscale = within(screen.getByRole("region", { name: "Tailscale" }))
  tailscale.getByText("Unknown")
  tailscale.getByText("100.64.0.2")
  expect(calls).toHaveLength(0)
})

test("UX-9: a failed host information read puts the error icon and its reason in each figure tile", async () => {
  stubFetch({ "GET /api/v1/hosts/h1/resources/hostinfo/summary": { status: 502, body: { type: "about:blank", title: "Bad gateway", status: 502, detail: "the agent did not answer", code: "agent.unreachable" } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1" })} />)
  const memory = within(screen.getByRole("region", { name: "Memory" }))
  await memory.findByRole("img", { name: "Memory unavailable" })
  for (const name of ["Disk", "Swap"]) within(screen.getByRole("region", { name })).getByRole("img", { name: `${name} unavailable` })
  screen.getByRole("group", { name: "Load per core: —" })
  screen.getByRole("group", { name: "Uptime: —" })
  await userEvent.click(memory.getByRole("button", { name: "Why memory is unavailable" }))
  expect((await screen.findAllByText("the agent did not answer")).length).toBeGreaterThan(0)
})

const on = (id: string, over: Partial<ModuleState> = {}): ModuleState => ({ id, version: 1, enabled: true, available: true, reason: null, checkedAt: null, ...over })

test("UI-4: the Docker, Firewall and Tailscale tiles show containers by state, rule count and connection", async () => {
  stubFetch({
    "GET /api/v1/hosts/h1/resources/docker/containers": { body: { containers: ["running", "running", "exited", "created"].map((state, i) => ({ id: `c${i}`, name: `c${i}`, image: "x", imageId: "x", state, status: state, created: "2026-11-19T10:00:00Z", ports: [] })) } },
    "GET /api/v1/hosts/h1/resources/firewall/status": { body: { supported: true, unsupported: [], active: true, inSync: true, outOfSync: [], configured: {}, loaded: {}, rules: [{}, {}] } },
    "GET /api/v1/hosts/h1/resources/tailscale/status": { body: { version: "1.80.0", backendState: "Running", self: { id: "n1", hostName: "web-1", dnsName: "web-1.ts.net.", os: "linux", addresses: ["100.64.0.2", "fd7a::2"], tags: [] }, tailnet: "t", magicDNSSuffix: "ts.net", preferredDerp: "fra", runSSH: false, peers: [], health: [] } },
  })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1", modules: [on("docker"), on("firewall"), on("tailscale")] })} />)
  const docker = within(screen.getByRole("region", { name: "Docker" }))
  await docker.findByRole("img", { name: "Containers: Running 2, Exited 1, Stopped 1" })
  const firewall = within(screen.getByRole("region", { name: "Firewall" }))
  await firewall.findByText("Active")
  expect(screen.getByRole("region", { name: "Firewall" }).textContent).toContain("2 rules")
  const tailscale = within(screen.getByRole("region", { name: "Tailscale" }))
  await tailscale.findByText("Connected")
  tailscale.getByText("100.64.0.2")
})

test("UI-4: each service tile says when its module is off, unavailable or predates the agent", async () => {
  const calls = stubFetch({})
  renderWithProviders(<HostOverview host={makeHost({ id: "h1", online: true, modules: [on("docker", { enabled: false }), on("tailscale", { available: false, reason: "tailscaled is not installed" })] })} />)
  within(screen.getByRole("region", { name: "Updates" })).getByText("Agent update needed")
  within(screen.getByRole("region", { name: "Docker" })).getByText("Not enabled")
  within(screen.getByRole("region", { name: "Firewall" })).getByText("Not enabled")
  const tailscale = within(screen.getByRole("region", { name: "Tailscale" }))
  tailscale.getByText("Unavailable")
  await userEvent.click(tailscale.getByRole("button", { name: "Why tailscale is unavailable" }))
  expect((await screen.findAllByText("tailscaled is not installed")).length).toBeGreaterThan(0)
  expect(calls.filter((c) => /\/(docker|firewall|tailscale|system)\//.test(c.path))).toHaveLength(0)
})
