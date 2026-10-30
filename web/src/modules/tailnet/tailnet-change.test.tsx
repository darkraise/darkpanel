import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useQuery } from "@tanstack/react-query"
import { expect, test } from "vitest"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { tailnetKey } from "./types"
import { TailnetChange } from "./tailnet-change"

const TC_PLAN = "POST /api/v1/actions/tailnet.authkey.create/v1/plan"
const TC_APPLY = "POST /api/v1/actions/tailnet.authkey.create/v1/apply"
const TC_INPUT = { tags: ["tag:server"], expirySeconds: 3600 }

function tcPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p1", hostId: null, action: "tailnet.authkey.create", version: 1, inputHash: "h",
    diff: [{ op: "+", text: "create an auth key with tag:server" }], guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null, ...over,
  }
}

const tcApplied = { id: "c1", hostId: null, state: "completed", class: "compensable", error: null, output: null, revertDeadline: null }

function TcHost({ bump }: { bump: (name: string) => number }) {
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  useQuery({ queryKey: tailnetKey("keys"), queryFn: () => Promise.resolve(bump("keys")) })
  useQuery({ queryKey: ["resource", "h1", "docker.engine"], queryFn: () => Promise.resolve(bump("engine")) })
  return (
    <>
      <button onClick={() => setInput(TC_INPUT)}>Open</button>
      <TailnetChange action="tailnet.authkey.create" title="Create an auth key" input={input} onClose={() => setInput(null)} />
    </>
  )
}

function tcRender(routes: Record<string, StubHandler>) {
  const calls = stubFetch({ "GET /api/v1/changes/c1": { body: { ...tcApplied, output: null } }, ...routes })
  const counts: Record<string, number> = {}
  const bump = (name: string) => (counts[name] = (counts[name] ?? 0) + 1)
  renderWithProviders(<TcHost bump={bump} />)
  return { calls, counts }
}

test("a null input opens no dialog and plans nothing", async () => {
  const { calls } = tcRender({ [TC_PLAN]: { body: tcPlan() } })
  await waitFor(() => expect(screen.getByRole("button", { name: "Open" })).toBeTruthy())
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls.filter((c) => c.method === "POST")).toHaveLength(0)
})

test("the dialog plans the action on the controller, without a host or an availability check", async () => {
  const { calls } = tcRender({ [TC_PLAN]: { body: tcPlan() } })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Create an auth key" })
  expect(within(dialog).getByText("tailnet.authkey.create/v1 on the controller")).toBeTruthy()
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ create an auth key with tag:server")
  const plans = calls.filter((c) => c.method === "POST")
  expect(plans.map((c) => [c.path, c.body])).toEqual([["/api/v1/actions/tailnet.authkey.create/v1/plan", { input: TC_INPUT }]])
  expect(calls.some((c) => c.path.includes("availability"))).toBe(false)
})

test("applying posts the plan without a revert timeout", async () => {
  const { calls } = tcRender({ [TC_PLAN]: { body: tcPlan() }, [TC_APPLY]: { body: tcApplied } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Create an auth key" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/actions/tailnet.authkey.create/v1/apply")).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
})

test("closing the dialog refetches the tailnet queries and no others", async () => {
  const { counts } = tcRender({ [TC_PLAN]: { body: tcPlan() } })
  const user = userEvent.setup()
  await waitFor(() => expect(counts).toEqual({ keys: 1, engine: 1 }))
  await user.click(screen.getByRole("button", { name: "Open" }))
  await screen.findByRole("dialog", { name: "Create an auth key" })
  expect(counts).toEqual({ keys: 1, engine: 1 })
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(counts.keys).toBe(2))
  expect(counts.engine).toBe(1)
})
