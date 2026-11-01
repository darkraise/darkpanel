import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { PackagePanel } from "./package-panel"

function ppRender(available: boolean) {
  const plan = (call: FetchCall) => {
    const action = call.path.split("/")[6] ?? ""
    return {
      body: {
        planId: "p1", hostId: "h1", action, version: 1, inputHash: "h",
        diff: [{ op: "~", text: action }], guards: [], expiresAt: "2026-10-31T09:00:00Z", confirmation: null,
      },
    }
  }
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/tailscale/availability": { body: { module: "tailscale", available: true, reason: null, actions: [] } },
    "POST /api/v1/hosts/h1/actions/tailscale.package.install/v1/plan": plan,
    "POST /api/v1/hosts/h1/actions/tailscale.package.update/v1/plan": plan,
  })
  const { container } = renderWithProviders(<PackagePanel hostId="h1" available={available} />)
  return { calls, container }
}

async function ppPlanned(calls: FetchCall[], title: string): Promise<{ path: string; body: unknown }> {
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  return { path: call?.path ?? "", body: call?.body }
}

test("a host where tailscaled does not answer offers the install and no update", async () => {
  const { calls } = ppRender(false)
  expect(screen.getByText("Tailscale is not installed on this host, or tailscaled does not answer.")).toBeTruthy()
  expect(screen.queryByRole("button", { name: "Update Tailscale" })).toBeNull()
  await userEvent.setup().click(screen.getByRole("button", { name: "Install Tailscale" }))
  expect(await ppPlanned(calls, "Install Tailscale")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.package.install/v1/plan",
    body: { input: {} },
  })
})

test("a host where tailscaled answers offers the update and no install", async () => {
  const { calls } = ppRender(true)
  expect(screen.queryByRole("button", { name: "Install Tailscale" })).toBeNull()
  await userEvent.setup().click(screen.getByRole("button", { name: "Update Tailscale" }))
  expect(await ppPlanned(calls, "Update Tailscale")).toEqual({
    path: "/api/v1/hosts/h1/actions/tailscale.package.update/v1/plan",
    body: { input: {} },
  })
})

test("the package panel has no accessibility violations", async () => {
  const { container } = ppRender(true)
  expect(await axe(container)).toHaveNoViolations()
})
