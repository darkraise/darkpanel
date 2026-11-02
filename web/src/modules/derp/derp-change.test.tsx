import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useQuery } from "@tanstack/react-query"
import { expect, test } from "vitest"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { derpKey } from "./types"
import { DerpChange } from "./derp-change"

const DC_HOST = "01JD0000000000000000000011"
const DC_PLAN = "POST /api/v1/actions/derp.server.upgrade/v1/plan"
const DC_APPLY = "POST /api/v1/actions/derp.server.upgrade/v1/apply"
const DC_INPUT = { hostId: DC_HOST }

function dcPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: null, action: "derp.server.upgrade", version: 1, inputHash: "h",
    diff: [{ op: "~", text: "upgrade derper on derp-1 from v1.102.0 to v1.104.1" }], guards: [], expiresAt: "2026-11-01T09:00:00Z", confirmation: null, ...over,
  }
}

const dcApplied = { id: "c1", hostId: null, state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function DcHost({ bump }: { bump: (name: string) => number }) {
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  useQuery({ queryKey: derpKey("servers"), queryFn: () => Promise.resolve(bump("servers")) })
  useQuery({ queryKey: derpKey("probe", DC_HOST), queryFn: () => Promise.resolve(bump("probe")) })
  useQuery({ queryKey: ["tailnet", "policy"], queryFn: () => Promise.resolve(bump("policy")) })
  useQuery({ queryKey: ["resource", "h1", "docker.engine"], queryFn: () => Promise.resolve(bump("engine")) })
  return (
    <>
      <button onClick={() => setInput(DC_INPUT)}>Open</button>
      <DerpChange action="derp.server.upgrade" title="Upgrade the DERP server" input={input} onClose={() => setInput(null)} />
    </>
  )
}

function dcRender(routes: Record<string, StubHandler>) {
  const calls = stubFetch({ "GET /api/v1/changes/c1": { body: { ...dcApplied, output: null } }, ...routes })
  const counts: Record<string, number> = {}
  const bump = (name: string) => (counts[name] = (counts[name] ?? 0) + 1)
  renderWithProviders(<DcHost bump={bump} />)
  return { calls, counts }
}

test("a null input opens no dialog and plans nothing", async () => {
  const { calls } = dcRender({ [DC_PLAN]: { body: dcPlan() } })
  await waitFor(() => expect(screen.getByRole("button", { name: "Open" })).toBeTruthy())
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(0)
})

test("the dialog plans the action on the controller at version 1, without a host or an availability check", async () => {
  const { calls } = dcRender({ [DC_PLAN]: { body: dcPlan() } })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Upgrade the DERP server" })
  expect(within(dialog).getByText("derp.server.upgrade/v1 on the controller")).toBeTruthy()
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("~ upgrade derper on derp-1 from v1.102.0 to v1.104.1")
  const plans = calls.filter((c) => c.method === "POST")
  expect(plans.map((c) => [c.path, c.body])).toEqual([["/api/v1/actions/derp.server.upgrade/v1/plan", { input: DC_INPUT }]])
  expect(calls.some((c) => c.path.includes("availability"))).toBe(false)
})

test("applying posts the plan without a revert timeout", async () => {
  const { calls } = dcRender({ [DC_PLAN]: { body: dcPlan() }, [DC_APPLY]: { body: dcApplied } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Upgrade the DERP server" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/actions/derp.server.upgrade/v1/apply")).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
})

test("closing the dialog refetches the derp queries and no others", async () => {
  const { counts } = dcRender({ [DC_PLAN]: { body: dcPlan() } })
  const user = userEvent.setup()
  await waitFor(() => expect(counts).toEqual({ servers: 1, probe: 1, policy: 1, engine: 1 }))
  await user.click(screen.getByRole("button", { name: "Open" }))
  await screen.findByRole("dialog", { name: "Upgrade the DERP server" })
  expect(counts).toEqual({ servers: 1, probe: 1, policy: 1, engine: 1 })
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(counts.servers).toBe(2))
  await waitFor(() => expect(counts.probe).toBe(2))
  expect([counts.policy, counts.engine]).toEqual([1, 1])
})
