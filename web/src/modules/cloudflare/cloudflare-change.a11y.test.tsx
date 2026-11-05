import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useState, type ReactElement } from "react"
import { describe, expect, test } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { CloudflareChange } from "./cloudflare-change"

interface CcVariant {
  name: string
  base: string // the path prefix of the action's plan and apply requests
  hostId: string | null
  action: string
  title: string
  class: string
  element: (open: boolean, close: () => void) => ReactElement
}

const ccVariants: CcVariant[] = [
  {
    name: "a controller action",
    hostId: null,
    base: "/api/v1/actions/cloudflare.expose.container/v1",
    action: "cloudflare.expose.container",
    title: "Expose web at app.example.com",
    class: "composite",
    element: (open, close) => (
      <CloudflareChange action="cloudflare.expose.container" title="Expose web at app.example.com" input={open ? { hostId: "h1", tunnel: "t1", container: "web", port: 80, network: "backend", hostname: "app.example.com" } : null} onClose={close} />
    ),
  },
  {
    name: "an agent action",
    hostId: "h1",
    base: "/api/v1/hosts/h1/actions/cloudflare.connector.stop/v1",
    action: "cloudflare.connector.stop",
    title: "Stop the connector of darkpanel-a",
    class: "compensable",
    element: (open, close) => (
      <CloudflareChange action="cloudflare.connector.stop" title="Stop the connector of darkpanel-a" hostId="h1" input={open ? { tunnel: "t1" } : null} onClose={close} />
    ),
  },
]

function ccHarness(variant: CcVariant) {
  return function CcHarness() {
    const [open, setOpen] = useState(false)
    return (
      <>
        <button type="button" onClick={() => setOpen(true)}>
          Open change
        </button>
        {variant.element(open, () => setOpen(false))}
      </>
    )
  }
}

function ccPlan(variant: CcVariant, confirmation: string | null): StubResponse {
  return { body: { planId: "p1", hostId: variant.hostId, action: variant.action, version: 1, inputHash: "h", diff: [{ op: "~", text: variant.action }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation } }
}

describe.each(ccVariants)("$name", (variant) => {
  const done = { id: "ch1", state: "completed", class: variant.class, error: null, output: null, revertDeadline: null }
  const states: { state: string; routes: Record<string, StubHandler>; ready: (d: HTMLElement) => Promise<unknown>; apply?: true }[] = [
    { state: "planning", routes: { [`POST ${variant.base}/plan`]: () => new Promise<StubResponse>(() => {}) }, ready: (d) => within(d).findByLabelText("Planning the change") },
    { state: "the plan", routes: {}, ready: (d) => within(d).findByLabelText("Planned changes") },
    {
      state: "a plan error",
      routes: { [`POST ${variant.base}/plan`]: { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "the tunnel changed", code: "cloud.conflict" } } },
      ready: (d) => within(d).findByText("the tunnel changed"),
    },
    { state: "the typed confirmation", routes: { [`POST ${variant.base}/plan`]: ccPlan(variant, "darkpanel-a") }, ready: (d) => within(d).findByLabelText("Type darkpanel-a to confirm") },
    { state: "the applied change", routes: {}, ready: (d) => within(d).findByText("completed"), apply: true },
  ]

  test.each(states)("$state has no axe violations, keeps Tab inside the dialog and gives focus back to its trigger", async ({ routes, ready, apply }) => {
    stubFetch({
      "GET /api/v1/hosts/h1/modules/cloudflare/availability": { body: { module: "cloudflare", available: true, reason: null, actions: [] } },
      [`POST ${variant.base}/plan`]: ccPlan(variant, null),
      [`POST ${variant.base}/apply`]: { body: done },
      "GET /api/v1/changes/ch1": { body: done },
      "GET /api/v1/changes/ch1/steps": { body: { steps: [] } },
      ...routes,
    })
    const Harness = ccHarness(variant)
    renderWithProviders(<Harness />)
    const user = userEvent.setup()
    await user.click(screen.getByRole("button", { name: "Open change" }))
    const dialog = await screen.findByRole("dialog", { name: variant.title })
    if (apply !== undefined) {
      const button = await within(dialog).findByRole("button", { name: "Apply" })
      await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false))
      await user.click(button)
    }
    await ready(dialog)
    await expectNoAxeViolations(dialog)
    for (let i = 0; i < 6; i++) {
      await user.tab()
      expect(dialog.contains(document.activeElement)).toBe(true)
    }
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Open change" }))
  })
})

test("an agent action on a host whose module is unavailable shows why and has no axe violations", async () => {
  const variant = ccVariants[1] as CcVariant
  stubFetch({
    "GET /api/v1/hosts/h1/modules/cloudflare/availability": { body: { module: "cloudflare", available: false, reason: "Docker is not installed", actions: [] } },
    [`POST ${variant.base}/plan`]: ccPlan(variant, null),
  })
  const Harness = ccHarness(variant)
  renderWithProviders(<Harness />)
  await userEvent.setup().click(screen.getByRole("button", { name: "Open change" }))
  const dialog = await screen.findByRole("dialog", { name: variant.title })
  await within(dialog).findByText("Docker is not installed")
  await expectNoAxeViolations(dialog)
})
