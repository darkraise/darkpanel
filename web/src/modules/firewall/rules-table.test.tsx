import { act, screen, waitFor, within } from "@testing-library/react"
import { useQuery } from "@tanstack/react-query"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { fwRule, fwStatus } from "./fixtures"
import { RulesTable } from "./rules-table"

const RT_PLAN = "POST /api/v1/hosts/h1/actions/firewall.rule.delete/v1/plan"

function rtPlan(): Plan {
  return {
    planId: "p1", hostId: "h1", action: "firewall.rule.delete", version: 1, inputHash: "h",
    diff: [{ op: "-", text: "allow in tcp from any to any port 22 (manual)" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null,
  }
}

function rtRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.delete", available: true, reason: null }] },
    },
    [RT_PLAN]: { body: rtPlan() },
    ...routes,
  })
  renderWithProviders(<RulesTable hostId="h1" rules={fwStatus().rules} />)
  return calls
}

function rtRow(n: number): HTMLElement {
  const rows = within(screen.getByRole("table", { name: "Firewall rules" })).getAllByRole("row")
  const row = rows[n]
  if (row === undefined) throw new Error(`no row ${n}`)
  return row
}

test("lists the rules in order with their origin", () => {
  rtRender()
  const cells = (n: number) => within(rtRow(n)).getAllByRole("cell").map((c) => c.textContent)
  expect(cells(1)).toEqual(["1", "allow", "in", "tcp", "any", "any port 443", "", "both", "off", "web", "darkpanel", "Delete"])
  expect(cells(2)).toEqual(["2", "allow", "in", "tcp", "any", "any port 22", "", "both", "off", "", "manual", "Delete"])
  expect(cells(3)).toEqual(["3", "allow any 22 0.0.0.0/0 any 0.0.0.0/0 in_eth0 -m owner", "read-only", ""])
})

test("shows ports, interfaces and logging", () => {
  stubFetch({})
  renderWithProviders(
    <RulesTable hostId="h1" rules={[fwRule({ direction: "route", from: "10.0.0.0/8", fromPort: "1024:2048", inIface: "eth0", outIface: "docker0", family: "v4", log: "log-all" })]} />,
  )
  const cells = within(rtRow(1)).getAllByRole("cell").map((c) => c.textContent)
  expect(cells.slice(1, 9)).toEqual(["allow", "route", "tcp", "10.0.0.0/8 port 1024:2048", "any port 443", "in on eth0, out on docker0", "v4", "log-all"])
})

test("deletes a Darkpanel rule by its id through the change dialog", async () => {
  const calls = rtRender()
  await userEvent.setup().click(screen.getByRole("button", { name: "Delete rule 1" }))
  const dialog = await screen.findByRole("dialog")
  expect(within(dialog).getByText("Delete rule: allow in tcp from any to any port 443").textContent).toBe("Delete rule: allow in tcp from any to any port 443")
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { id: "01JD0000000000000000000001" } })
})

test("a manual rule is deleted only after the operator agrees, with manual: true", async () => {
  const calls = rtRender()
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Delete rule 2" }))
  const ask = await screen.findByRole("dialog")
  expect(within(ask).getByText("Delete a manual rule?").textContent).toBe("Delete a manual rule?")
  await user.click(within(ask).getByRole("button", { name: "Cancel" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)

  await user.click(screen.getByRole("button", { name: "Delete rule 2" }))
  await user.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Delete it anyway" }))
  const dialog = await screen.findByRole("dialog", { name: "Delete rule: allow in tcp from any to any port 22" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("- allow in tcp from any to any port 22 (manual)")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { id: "manual-0123456789abcdef", manual: true } })
})

test("a rule the model cannot express offers no delete", () => {
  rtRender()
  expect(screen.queryByRole("button", { name: "Delete rule 3" })).toBeNull()
})

test("no rules is an empty state", () => {
  stubFetch({})
  renderWithProviders(<RulesTable hostId="h1" rules={[]} />)
  expect(screen.getByText("No rules").textContent).toBe("No rules")
})

test("the rules table has no accessibility violations", async () => {
  rtRender()
  expect(await axe(screen.getByRole("table", { name: "Firewall rules" }))).toHaveNoViolations()
})

function RtLive() {
  const rules = useQuery({ queryKey: ["rt-rules"], queryFn: () => fwStatus().rules, initialData: fwStatus().rules, staleTime: Infinity }).data
  return <RulesTable hostId="h1" rules={rules} />
}

test("the delete dialog stays open when the last rule goes away", async () => {
  stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.delete", available: true, reason: null }] },
    },
    [RT_PLAN]: { body: rtPlan() },
  })
  const { client } = renderWithProviders(<RtLive />)
  await userEvent.setup().click(screen.getByRole("button", { name: "Delete rule 1" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  act(() => {
    client.setQueryData(["rt-rules"], [])
  })
  expect((await screen.findByText("No rules")).textContent).toBe("No rules")
  expect(screen.getByRole("dialog", { name: "Delete rule: allow in tcp from any to any port 443" })).toBe(dialog)
})
