import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import type { Rollout } from "@/api/types"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { RolloutsPage } from "./rollouts"

const ROLLOUTS = "POST /api/v1/actions/core.update.rollouts/v1/read"
const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const CANCEL_PLAN = "POST /api/v1/actions/core.update.rollout-cancel/v1/plan"
const CANCEL_APPLY = "POST /api/v1/actions/core.update.rollout-cancel/v1/apply"

function rollout(over: Partial<Rollout> = {}): Rollout {
  return {
    id: "r1", version: "1.2.3", channel: "stable", state: "running", error: null,
    createdAt: "2026-11-11T08:00:00Z", updatedAt: "2026-11-11T08:01:00Z",
    hosts: [
      { hostId: "h1", position: 0, state: "committed", planId: null, operationId: null, error: null },
      { hostId: "gone", position: 1, state: "updating", planId: null, operationId: null, error: null },
    ],
    ...over,
  }
}

afterEach(() => vi.useRealTimers())

test("UI-3: each rollout shows its version, state and ordered hosts by name", async () => {
  stubFetch({ [ROLLOUTS]: { body: { output: { rollouts: [rollout()] } } }, [HOSTS]: { body: { output: { hosts: [makeHost({ id: "h1", name: "web-1" })] } } } })
  renderWithProviders(<RolloutsPage />)
  const card = within(await screen.findByRole("region", { name: "1.2.3 (stable)" }))
  card.getByText("running")
  const rows = card.getAllByRole("row").slice(1).map((r) => r.textContent)
  expect(rows[0]).toContain("1web-1committed")
  expect(rows[1]).toContain("2goneupdating")
})

test("UX-8: an empty list offers to start one", async () => {
  stubFetch({ [ROLLOUTS]: { body: { output: { rollouts: [] } } }, [HOSTS]: { body: { output: { hosts: [] } } } })
  renderWithProviders(<RolloutsPage />)
  await screen.findByText("No rollouts yet")
})

test("UX-8: the list re-reads every 5 seconds only while a rollout is running", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  let n = 0
  stubFetch({
    [ROLLOUTS]: () => ({ body: { output: { rollouts: [rollout({ state: n++ === 0 ? "running" : "succeeded" })] } } }),
    [HOSTS]: { body: { output: { hosts: [] } } },
  })
  renderWithProviders(<RolloutsPage />)
  await screen.findByText("running")
  await act(async () => { await vi.advanceTimersByTimeAsync(5000) })
  await screen.findByText("succeeded")
  await act(async () => { await vi.advanceTimersByTimeAsync(15000) })
  expect(n).toBe(2)
})

test("UX-10: cancelling a running rollout sends force and refreshes", async () => {
  const calls = stubFetch({
    [ROLLOUTS]: { body: { output: { rollouts: [rollout()] } } },
    [HOSTS]: { body: { output: { hosts: [] } } },
    [CANCEL_PLAN]: { body: makePlan("core.update.rollout-cancel") },
    [CANCEL_APPLY]: { body: makeChange("core.update.rollout-cancel", rollout({ state: "cancelled" })) },
  })
  renderWithProviders(<RolloutsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Cancel rollout" }))
  const force = screen.getByRole("checkbox", { name: "Cancel without waiting for the host that is updating" })
  await waitFor(() => expect(document.activeElement).toBe(force))
  await user.click(force)
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  await user.click(await within(dialog).findByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  expect(calls.find((c) => c.path.endsWith("cancel/v1/plan"))?.body).toEqual({ input: { rolloutId: "r1", force: true } })
  await waitFor(() => expect(calls.filter((c) => c.path.endsWith("rollouts/v1/read")).length).toBeGreaterThan(1))
})

test("UX-10: a finished rollout has no cancel button", async () => {
  stubFetch({ [ROLLOUTS]: { body: { output: { rollouts: [rollout({ state: "failed", error: { code: "rollout.host_failed", message: "web-2 failed its health check" } })] } } }, [HOSTS]: { body: { output: { hosts: [] } } } })
  renderWithProviders(<RolloutsPage />)
  await screen.findByText("web-2 failed its health check")
  expect(screen.queryByRole("button", { name: "Cancel rollout" })).toBeNull()
})

test("UX-12: each rollout's host table scrolls inside its own container", async () => {
  stubFetch({ [ROLLOUTS]: { body: { output: { rollouts: [rollout()] } } }, [HOSTS]: { body: { output: { hosts: [] } } } })
  renderWithProviders(<RolloutsPage />)
  const table = await screen.findByRole("table")
  expect(table.closest(".overflow-x-auto")).not.toBeNull()
})
