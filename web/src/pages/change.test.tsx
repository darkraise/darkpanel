import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Change, Effect, ObserveResult, Step } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { ChangePage } from "./change"

function cpgChange(over: Partial<Change> = {}): Change {
  return {
    id: "c1", hostId: "h1", action: "cdx.rule.set", version: 1, class: "compensable", state: "needs_repair", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:05:00Z", revertDeadline: null, output: null,
    error: { code: "core.needs_repair", message: "the provider's answer was lost", cause: "cloud.uncertain_outcome" },
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function cpgEffect(over: Partial<Effect> = {}): Effect {
  return {
    changeId: "c1", seq: 1, kind: "create", resource: "cdx.key:?darkpanel-c1", local: false, inverseLocal: false,
    commitBy: "2026-10-23T08:10:00Z", state: "unresolved", reason: "uncertain", before: { state: null }, after: { state: { tags: ["a"] } },
    post: null, undoTo: null, undoResource: "", current: null, resolution: "", resolvedBy: "", resolvedAt: null, ...over,
  }
}

function cpgObserve(over: Partial<ObserveResult["observation"]> = {}, allowed: ObserveResult["allowed"] = null): ObserveResult {
  return {
    effect: cpgEffect(),
    observation: { current: { state: { tags: ["a"] } }, resource: "cdx.key:k1", matches: 1, candidates: null, exclusive: true, ...over },
    allowed,
  }
}

function cpgRender(routes: Record<string, StubHandler>, path = "/changes/c1") {
  const calls = stubFetch(routes)
  renderWithProviders(
    <Routes>
      <Route path="/changes/:changeId" element={<ChangePage />} />
    </Routes>,
    { path },
  )
  return calls
}

test("shows the change with its error, cause and links", async () => {
  cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange({ parentChangeId: "p9", repairedBy: "r7" }) },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
  })
  expect((await screen.findByRole("heading", { level: 1, name: "cdx.rule.set/v1" })).textContent).toBe("cdx.rule.set/v1")
  expect(screen.getByText("needs_repair").textContent).toBe("needs_repair")
  expect(screen.getByRole("alert").textContent).toBe("core.needs_repair: the provider's answer was lostCause: cloud.uncertain_outcome")
  expect(screen.getByRole("link", { name: "p9" }).getAttribute("href")).toBe("/changes/p9")
  expect(screen.getByRole("link", { name: "r7" }).getAttribute("href")).toBe("/changes/r7")
  expect((await screen.findByText("This change recorded no effects.")).textContent).toBe("This change recorded no effects.")
})

test("a composite lists its steps in order with links to their changes", async () => {
  const steps: Step[] = [
    { id: "ports", ordinal: 2, host: "h1", action: "cdx.ports.open/v1", state: "pending", attempt: 0, childChangeId: "", earlierChildChangeIds: null, childDeadline: null, error: null },
    { id: "network", ordinal: 1, host: "h1", action: "cdx.net.create/v1", state: "applied", attempt: 1, childChangeId: "k1", earlierChildChangeIds: null, childDeadline: null, error: null },
  ]
  cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange({ class: "composite", error: null, state: "applying" }) },
    "GET /api/v1/changes/c1/steps": { body: { steps } },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
  })
  const section = await screen.findByRole("region", { name: "Steps" })
  const rows = await within(section).findAllByRole("row")
  expect(rows.slice(1).map((r) => within(r).getAllByRole("cell")[1]?.textContent)).toEqual(["network", "ports"])
  expect(within(section).getByRole("link", { name: "k1" }).getAttribute("href")).toBe("/changes/k1")
})

test("an unresolved effect is previewed before it is accepted", async () => {
  const calls = cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange() },
    "GET /api/v1/changes/c1/effects": () => ({
      body: { effects: [calls.some((c) => c.path.endsWith("/resolve")) ? cpgEffect({ state: "accepted", resolution: "accept", resolvedBy: "user:u1" }) : cpgEffect()] },
    }),
    "GET /api/v1/changes/c1/effects/1/observe": { body: cpgObserve() },
    "POST /api/v1/changes/c1/effects/1/resolve": { body: cpgEffect({ state: "accepted", resolution: "accept" }) },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  expect((await within(dialog).findByLabelText("Now")).textContent).toBe('{\n  "tags": [\n    "a"\n  ]\n}')
  expect(within(dialog).getByLabelText("Before").textContent).toBe("null")
  await user.click(within(dialog).getByRole("button", { name: "Accept as it is" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(calls.find((c) => c.path.endsWith("/resolve"))?.body).toEqual({ resolution: "accept" })
  expect((await screen.findByText("accept by user:u1")).textContent).toBe("accept by user:u1")
})

test("several matching objects need a choice before reconcile, and allowed limits the resolutions", async () => {
  const calls = cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange() },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpgEffect()] } },
    "GET /api/v1/changes/c1/effects/1/observe": {
      body: cpgObserve(
        {
          matches: 2, resource: "", exclusive: false,
          candidates: [{ resource: "cdx.key:k1", state: { state: {} } }, { resource: "cdx.key:k2", state: { state: {} } }],
        },
        ["reconcile"],
      ),
    },
    "POST /api/v1/changes/c1/effects/1/resolve": { body: cpgEffect({ state: "made", resolution: "reconcile" }) },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  const reconcile = await within(dialog).findByRole("button", { name: "Reconcile" })
  expect(within(dialog).queryByRole("button", { name: "Accept as it is" })).toBeNull()
  expect(reconcile.hasAttribute("disabled")).toBe(true)
  await user.click(within(dialog).getByRole("radio", { name: "cdx.key:k2" }))
  await user.click(reconcile)
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("/resolve"))?.body).toEqual({ resolution: "reconcile", resource: "cdx.key:k2" }))
})

test("a refused resolution keeps the dialog open with the reason", async () => {
  cpgRender({
    "GET /api/v1/changes/c1": { body: cpgChange() },
    "GET /api/v1/changes/c1/effects": { body: { effects: [cpgEffect()] } },
    "GET /api/v1/changes/c1/effects/1/observe": { body: cpgObserve() },
    "POST /api/v1/changes/c1/effects/1/resolve": {
      status: 409,
      body: { type: "about:blank", title: "Change busy", status: 409, detail: "resolve after 08:10", code: "core.change_busy" },
    },
  })
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Preview effect 1" }))
  const dialog = await screen.findByRole("dialog", { name: "Resolve effect 1" })
  await user.click(await within(dialog).findByRole("button", { name: "Accept as it is" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("resolve after 08:10")
})

test("an unknown change shows the API's error", async () => {
  cpgRender({
    "GET /api/v1/changes/zz": {
      status: 404,
      body: { type: "about:blank", title: "Change not found", status: 404, detail: "no change zz", code: "change.not_found" },
    },
  }, "/changes/zz")
  expect((await screen.findByRole("alert")).textContent).toBe("no change zz")
})
