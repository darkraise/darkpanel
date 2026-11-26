import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { swapDefault, swapUnsupported, sysctlDefault } from "@/modules/m11-fixtures"
import { makePlan, renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { TuningPage } from "./tuning-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const ok = (output: unknown): StubResponse => ({ body: { output } })
const refused = (detail: string): StubResponse => ({ status: 422, body: { type: "about:blank", title: detail, status: 422, detail, code: "request.invalid_input" } })
const firewallOwned = { ...sysctlDefault, keys: sysctlDefault.keys.map((k) => (k.owner === "docker" ? { ...k, owner: "firewall" } : k)) }

function render(routes: Record<string, StubHandler> = {}, { swap = swapDefault as object, sysctl = firewallOwned as object, scope = "admin" as Scope } = {}) {
  const calls = stubFetch({
    [`${BASE}/tuning.swap.get/v1/read`]: ok(swap),
    [`${BASE}/tuning.sysctl.get/v1/read`]: ok(sysctl),
    "GET /api/v1/hosts/h1/modules/tuning/availability": { body: { module: "tuning", available: true, reason: null, actions: [] } },
    ...routes,
  })
  renderWithProviders(<TuningPage hostId="h1" />, { hostScope: scope })
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("tuning flows", () => {
  test("vm.swappiness=300 is refused inline with the range, and Review changes waits", async () => {
    render()
    const user = userEvent.setup()
    const field = await screen.findByLabelText("vm.swappiness at boot")
    await user.clear(field)
    await user.type(field, "300")
    expect(screen.getByText("Use 0 to 200")).toBeTruthy()
    expect(field.getAttribute("aria-invalid")).toBe("true")
    expect(screen.getByRole("button", { name: "Review changes" })).toHaveProperty("disabled", true)
    await user.clear(field)
    await user.type(field, "20")
    expect(screen.queryByText("Use 0 to 200")).toBeNull()
    expect(screen.getByRole("button", { name: "Review changes" })).toHaveProperty("disabled", false)
  })

  test("a firewall-owned key shows Managed by firewall and is disabled", async () => {
    render()
    expect(await screen.findByText("Managed by firewall")).toBeTruthy()
    expect(screen.getByLabelText("net.ipv4.ip_forward at boot")).toHaveProperty("disabled", true)
  })

  test("a key a later file sets shows the boot-override warning naming it", async () => {
    render()
    expect(await screen.findByText("/etc/sysctl.d/99-zzz-local.conf sets it later at boot and wins")).toBeTruthy()
  })

  test("one Review changes button plans every changed key in one revertible change", async () => {
    const plans: unknown[] = []
    render({
      [`${BASE}/tuning.sysctl.apply/v1/plan`]: (c) => {
        plans.push(c.body)
        return { body: makePlan("tuning.sysctl.apply", { hostId: "h1" }) }
      },
    })
    const user = userEvent.setup()
    await user.type(await screen.findByLabelText("net.core.somaxconn at boot"), "8192")
    await user.type(screen.getByLabelText("net.ipv4.ip_local_port_range at boot"), "10000   65000")
    await user.type(screen.getByLabelText("net.ipv4.tcp_congestion_control at boot"), "bbr")
    expect(screen.getByText("3 changed")).toBeTruthy()
    await user.click(screen.getByRole("button", { name: "Review changes" }))
    expect(await screen.findByRole("dialog", { name: "Set 3 kernel parameters" })).toBeTruthy()
    await screen.findByRole("button", { name: "Apply" })
    expect(plans).toHaveLength(1)
    const body = plans[0] as { input: { values: Record<string, string> }; confirmTimeoutSeconds?: number }
    expect(body.input.values).toEqual({
      "vm.swappiness": "10",
      "net.core.somaxconn": "8192",
      "net.ipv4.ip_local_port_range": "10000 65000",
      "net.ipv4.tcp_congestion_control": "bbr",
    })
  })

  test("swap remove refused shows the guard text", async () => {
    const guard = "/darkpanel.swap holds 900 MiB of swapped memory, more than the 700 MiB available less 256 MiB; free memory first"
    render({ [`${BASE}/tuning.swap.remove/v1/plan`]: refused(guard) })
    await userEvent.setup().click(await screen.findByRole("button", { name: "Remove" }))
    const dialog = await screen.findByRole("dialog", { name: "Remove the swap file" })
    expect(await within(dialog).findByText(guard)).toBeTruthy()
  })

  test("a resize outside the free space is refused inline; a valid one plans tuning.swap.resize", async () => {
    const plans: unknown[] = []
    render({
      [`${BASE}/tuning.swap.resize/v1/plan`]: (c) => {
        plans.push(c.body)
        return { body: makePlan("tuning.swap.resize", { hostId: "h1" }) }
      },
    })
    const user = userEvent.setup()
    const size = await screen.findByLabelText("/darkpanel.swap size (MiB)")
    expect(size).toHaveProperty("value", "1024")
    await user.clear(size)
    await user.type(size, "100")
    expect(screen.getByText("Use 256 to 38912 MiB")).toBeTruthy()
    await user.clear(size)
    await user.type(size, "2048{Enter}")
    await screen.findByRole("button", { name: "Apply" })
    expect(plans).toEqual([{ input: { sizeMiB: 2048 } }])
  })

  test("a btrfs root shows the swap file unavailable with btrfs, and no swap actions", async () => {
    render({}, { swap: swapUnsupported })
    expect(await screen.findByText(/btrfs/)).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Create swap file" })).toBeNull()
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull()
    expect(screen.getByRole("progressbar", { name: "/dev/sda3 used" })).toBeTruthy()
  })

  test("a viewer sees the values without inputs or actions", async () => {
    render({}, { scope: "read" })
    await screen.findByRole("table", { name: "Kernel parameters" })
    expect(screen.queryByRole("textbox")).toBeNull()
    expect(screen.queryByRole("button", { name: "Review changes" })).toBeNull()
    expect(screen.queryByRole("button", { name: "Resize" })).toBeNull()
  })
})
