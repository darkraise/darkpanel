import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change, Effect, ObserveResult, Step } from "@/api/types"
import { expectNoAxeViolations, focusedName } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangePage } from "./change"

function cpaChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "needs_repair", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null,
    error: { code: "core.needs_repair", message: "the provider's answer was lost", cause: "cloud.uncertain_outcome" },
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cpaEffect(over: Partial<Effect> = {}): Effect {
  return {
    changeId: "c1", seq: 1, kind: "create", resource: "cdx.key:?darkpanel-c1", local: false, inverseLocal: false,
    commitBy: "2026-10-23T08:10:00Z", state: "unresolved", reason: "uncertain", before: { state: null }, after: { state: { tags: ["a"] } },
    post: null, undoTo: null, undoResource: "", current: null, resolution: "", resolvedBy: "", resolvedAt: null, ...over,
  }
}

const cpaTwoMatches: ObserveResult = {
  effect: cpaEffect(),
  observation: {
    current: { state: { tags: ["a"] } }, resource: "", matches: 2, exclusive: false,
    candidates: [{ resource: "cdx.key:k1", state: { state: {} } }, { resource: "cdx.key:k2", state: { state: {} } }],
  },
  allowed: null,
}

const cpaSteps: Step[] = [
  { id: "ports", ordinal: 2, host: "h1", action: "cdx.ports.open/v1", state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
  { id: "network", ordinal: 1, host: "h1", action: "cdx.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
]

function cpaRender(routes: Record<string, StubHandler>, path = "/changes/c1") {
  stubFetch(routes)
  return renderWithProviders(
    <Routes>
      <Route path="/changes/:changeId" element={<ChangePage />} />
    </Routes>,
    { path },
  )
}

test("a composite that needs repair, with its steps, effects and actions, has no violations", async () => {
  const { container } = cpaRender({
    "GET /api/v1/changes/c1": { body: cpaChange({ class: "composite" }) },
    "GET /api/v1/changes/c1/steps": { body: { steps: cpaSteps } },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpaEffect(), cpaEffect({ seq: 2, state: "accepted", resolution: "accept", resolvedBy: "user:u1" })] } },
  })
  await screen.findByRole("region", { name: "Steps" })
  await screen.findByRole("region", { name: "Effects" })
  await screen.findByRole("button", { name: "Preview effect 1" })
  await screen.findByRole("group", { name: "Decide a step" })
  await expectNoAxeViolations(container)
})

test("a completed change with no effects has no violations", async () => {
  const { container } = cpaRender({
    "GET /api/v1/changes/c1": { body: cpaChange({ class: "revertible", state: "confirmed", error: null }) },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
  })
  await screen.findByText("This change recorded no effects.")
  await expectNoAxeViolations(container)
})

test("the resolve dialog has no violations, and Escape returns focus to the effect's button", async () => {
  cpaRender({
    "GET /api/v1/changes/c1": { body: cpaChange() },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpaEffect()] } },
    "GET /api/v1/changes/c1/effects/1/observe": { body: cpaTwoMatches },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  await within(dialog).findByRole("radio", { name: "cdx.key:k2" })
  await expectNoAxeViolations(dialog)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Preview effect 1")
})

test("the page title names the change by the last eight characters of its id and is restored after", async () => {
  const id = "01JZ8Q4M6N3V7B2D5F9H1K0XYZ"
  document.title = "before"
  const { unmount } = cpaRender(
    {
      [`GET /api/v1/changes/${id}`]: { body: cpaChange({ id, error: null, state: "completed" }) },
      [`GET /api/v1/changes/${id}/effects`]: { body: { effects: [] } },
    },
    `/changes/${id}`,
  )
  expect(document.title).toBe("Change 9H1K0XYZ · Darkpanel")
  await screen.findByText("This change recorded no effects.")
  expect(document.title).toBe("Change 9H1K0XYZ · Darkpanel")
  unmount()
  expect(document.title).toBe("before")
})

test("an id of eight characters or fewer is the whole title, also while the change is unknown", async () => {
  cpaRender(
    { "GET /api/v1/changes/zz": { status: 404, body: { type: "about:blank", title: "Change not found", status: 404, detail: "no change zz", code: "change.not_found" } } },
    "/changes/zz",
  )
  expect(document.title).toBe("Change zz · Darkpanel")
  await screen.findByRole("alert")
  expect(document.title).toBe("Change zz · Darkpanel")
})
