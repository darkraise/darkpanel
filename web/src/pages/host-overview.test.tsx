import { screen, within } from "@testing-library/react"
import { expect, test } from "vitest"
import { formatTime } from "@/lib/format"
import { makeHost, renderWithProviders, stubFetch } from "@/test/utils"
import { HostOverview } from "./host-overview"

const STATUS = "POST /api/v1/hosts/h1/actions/core.update.status/v1/read"
const tx = {
  id: "t1", operationId: "t1", parentId: null, kind: "update", fromVersion: "1.0.0", toVersion: "1.1.0", targetVersion: "1.1.0",
  state: "rolled-back", error: { code: "update.health_failed", message: "the health check failed" },
  startedAt: "2026-11-11T07:00:00Z", updatedAt: "2026-11-11T07:05:00Z",
}

test("UI-2: shows the Host, Updates and Enrollment and removal cards with the host's facts", async () => {
  stubFetch({ [STATUS]: { body: { output: { transactions: [] } } } })
  renderWithProviders(<HostOverview host={makeHost({ id: "h1", agentVersion: "1.2.0", protocolVersion: 3, os: "ubuntu-24.04", arch: "amd64" })} />)
  expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Host", "Updates", "Enrollment and removal"])
  const facts = within(screen.getByRole("region", { name: "Host" }))
  facts.getByText("Enrolled")
  facts.getByText("1.2.0")
  facts.getByText("3")
  facts.getByText("ubuntu-24.04")
  facts.getByText("amd64")
  facts.getByText(`Online, last seen ${formatTime("2026-11-11T08:00:00Z")}`)
})

test("UI-2: a pending host shows — for every fact its agent has not reported", () => {
  stubFetch({})
  renderWithProviders(<HostOverview host={makeHost({ state: "pending", online: false, lastSeenAt: null, agentVersion: null, protocolVersion: null, os: null, arch: null })} />)
  const facts = within(screen.getByRole("region", { name: "Host" }))
  ;["Agent version", "Protocol version", "OS", "Architecture", "Supported"].forEach((label) => expect(facts.getByText(label).nextElementSibling?.textContent).toBe("—"))
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
