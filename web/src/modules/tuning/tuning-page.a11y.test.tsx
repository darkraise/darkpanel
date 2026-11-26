import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { swapDefault, swapUnsupported, sysctlDefault } from "@/modules/m11-fixtures"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { TuningPage } from "./tuning-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const THEMES = ["light", "dark"] as const
const ok = (output: unknown): StubResponse => ({ body: { output } })
const failed: StubHandler = { status: 500, body: { code: "internal", detail: "boom", status: 500 } }
const noOwnSwap = { ...swapDefault, swaps: [swapDefault.swaps[0]] }

// Each state: the swap and kernel parameter answers, the caller's scope and
// how to reach it.
const STATES: [string, StubHandler, StubHandler, Scope, () => Promise<void>][] = [
  ["loading", () => new Promise(() => {}), () => new Promise(() => {}), "admin", async () => {
    await screen.findByRole("status", { name: "Loading kernel parameters" })
  }],
  ["error", failed, failed, "admin", async () => {
    await screen.findByText("Couldn't load kernel parameters")
  }],
  ["default with locked key and override warning", ok(swapDefault), ok(sysctlDefault), "admin", async () => {
    await screen.findByText("Managed by docker")
    await screen.findByText("/etc/sysctl.d/99-zzz-local.conf sets it later at boot and wins")
  }],
  ["invalid value", ok(swapDefault), ok(sysctlDefault), "admin", async () => {
    const user = userEvent.setup()
    const field = await screen.findByLabelText("vm.swappiness at boot")
    await user.clear(field)
    await user.type(field, "300")
    await screen.findByText("Use 0 to 200")
  }],
  ["no Darkpanel swap", ok(noOwnSwap), ok(sysctlDefault), "admin", async () => {
    await screen.findByRole("button", { name: "Create swap file" })
  }],
  ["unsupported filesystem", ok(swapUnsupported), ok(sysctlDefault), "admin", async () => {
    await screen.findByText(/btrfs/)
  }],
  ["viewer", ok(swapDefault), ok(sysctlDefault), "read", async () => {
    await screen.findByRole("table", { name: "Kernel parameters" })
  }],
  ["swap remove refused", ok(swapDefault), ok(sysctlDefault), "admin", async () => {
    await userEvent.setup().click(await screen.findByRole("button", { name: "Remove" }))
    await screen.findByText(/free memory first/)
  }],
]

afterEach(() => {
  document.documentElement.classList.remove("dark")
  vi.unstubAllGlobals()
})

describe("no axe violations in any state", () => {
  for (const theme of THEMES) {
    test.each(STATES)(`%s (${theme})`, async (_name, swap, sysctl, scope, reach) => {
      document.documentElement.classList.toggle("dark", theme === "dark")
      stubFetch({
        [`${BASE}/tuning.swap.get/v1/read`]: swap,
        [`${BASE}/tuning.sysctl.get/v1/read`]: sysctl,
        [`${BASE}/tuning.swap.remove/v1/plan`]: {
          status: 422,
          body: { type: "about:blank", title: "refused", status: 422, detail: "/darkpanel.swap holds 900 MiB of swapped memory; free memory first", code: "request.invalid_input" },
        },
        "GET /api/v1/hosts/h1/modules/tuning/availability": { body: { module: "tuning", available: true, reason: null, actions: [] } },
      })
      const { container } = renderWithProviders(<TuningPage hostId="h1" />, { hostScope: scope })
      await reach()
      await expectNoAxeViolations(container)
      for (const dialog of document.querySelectorAll("[role=dialog]")) await expectNoAxeViolations(dialog)
    }, 20_000)
  }
})
