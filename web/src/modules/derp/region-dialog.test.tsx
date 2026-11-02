import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubResponse } from "@/test/utils"
import { probeFixture } from "./fixtures"
import { RegionDialog } from "./region-dialog"
import type { DerpServerView, ServerProbe } from "./types"

const RD_PLAN = "POST /api/v1/actions/derp.region.set/v1/plan"

const rdServer: DerpServerView = {
  hostId: "h2",
  host: "derp-1",
  mode: "ip",
  hostName: "203.0.113.10",
  regionId: 0,
  regionCode: "",
  derperVersion: "v1.104.1",
  certName: "sha256-raw:ab",
  certNotAfter: "2027-06-01T00:00:00Z",
  unitActive: true,
  health: "ok",
  reasons: [],
  checkedAt: "2026-11-02T11:59:00Z",
  error: "",
}

const rdProbe: ServerProbe = { ...probeFixture, hostId: "h2", ready: true, warnings: [] }

function rdPlan(): StubResponse {
  return {
    body: {
      planId: "p1",
      hostId: null,
      action: "derp.region.set",
      version: 1,
      inputHash: "h",
      diff: [{ op: "~", text: "publish the region" }],
      guards: [],
      expiresAt: "2026-11-02T13:00:00Z",
      confirmation: null,
    } satisfies Plan,
  }
}

function rdPlans(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("derp.region.set/v1/plan")).map((c) => c.body)
}

function rdOpen(over: { server?: DerpServerView; probe?: ServerProbe; onClose?: () => void } = {}) {
  renderWithProviders(<RegionDialog server={over.server ?? rdServer} probe={over.probe ?? rdProbe} onClose={over.onClose ?? (() => {})} />)
  return screen.findByRole("dialog", { name: "Publish a DERP region on derp-1" })
}

async function rdFill(user: ReturnType<typeof userEvent.setup>, dialog: HTMLElement, code: string, name: string) {
  await user.type(within(dialog).getByLabelText("Region code"), code)
  await user.type(within(dialog).getByLabelText("Region name"), name)
}

test("starts at region 900, keeps Review off until the code and name are valid, and plans derp.region.set", async () => {
  const calls = stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen()
  const user = userEvent.setup()
  const review = within(dialog).getByRole("button", { name: "Review" })
  expect((within(dialog).getByLabelText("Region id") as HTMLInputElement).value).toBe("900")
  expect(review.hasAttribute("disabled")).toBe(true)

  await rdFill(user, dialog, "fra-x", "Frankfurt X")
  await waitFor(() => expect(review.hasAttribute("disabled")).toBe(false))
  await user.click(review)

  const change = await screen.findByRole("dialog", { name: "Publish region fra-x" })
  expect((await within(change).findByLabelText("Planned changes")).textContent).toBe("~ publish the region")
  expect(screen.queryByRole("dialog", { name: "Publish a DERP region on derp-1" })).toBeNull()
  expect(rdPlans(calls)).toEqual([{ input: { hostId: "h2", regionId: 900, regionCode: "fra-x", regionName: "Frankfurt X" } }])
})

test("an existing region starts from the published id and code", async () => {
  stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen({ server: { ...rdServer, regionId: 905, regionCode: "ams-x" } })
  expect((within(dialog).getByLabelText("Region id") as HTMLInputElement).value).toBe("905")
  expect((within(dialog).getByLabelText("Region code") as HTMLInputElement).value).toBe("ams-x")
})

const rdInvalid = [
  { what: "an id below 900", field: "Region id", value: "899" },
  { what: "an id above 999", field: "Region id", value: "1000" },
  { what: "an id that is not a number", field: "Region id", value: "" },
  { what: "a code with a capital letter", field: "Region code", value: "Fra" },
  { what: "a code that starts with a digit", field: "Region code", value: "9fra" },
  { what: "a code over 32 characters", field: "Region code", value: "a".repeat(33) },
  { what: "a name over 64 characters", field: "Region name", value: "n".repeat(65) },
]

test.each(rdInvalid)("Review stays off for $what", async ({ field, value }) => {
  stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen()
  const user = userEvent.setup()
  await rdFill(user, dialog, "fra-x", "Frankfurt X")
  const input = within(dialog).getByLabelText(field)
  await user.clear(input)
  if (value !== "") await user.type(input, value)
  await waitFor(() => expect(within(dialog).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true))
})

test("omitting the default regions asks for the acknowledgement and sends what was typed", async () => {
  const calls = stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen()
  const user = userEvent.setup()
  await rdFill(user, dialog, "fra-x", "Frankfurt X")
  expect(within(dialog).queryByLabelText("Acknowledgement")).toBeNull()

  await user.click(within(dialog).getByRole("checkbox", { name: "Omit default regions" }))
  const ack = within(dialog).getByLabelText("Acknowledgement")
  expect(within(dialog).getByText(/nodes will relay only through fra-x/)).toBeTruthy()
  await user.type(ack, "nodes will relay only through fra-x")
  await user.click(within(dialog).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Publish region fra-x" })
  expect(rdPlans(calls)).toEqual([
    { input: { hostId: "h2", regionId: 900, regionCode: "fra-x", regionName: "Frankfurt X", omitDefaultRegions: true, acknowledge: "nodes will relay only through fra-x" } },
  ])
})

test("omitting the default regions without an acknowledgement leaves the decision to the server", async () => {
  const calls = stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen()
  const user = userEvent.setup()
  await rdFill(user, dialog, "fra-x", "Frankfurt X")
  await user.click(within(dialog).getByRole("checkbox", { name: "Omit default regions" }))
  await user.click(within(dialog).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Publish region fra-x" })
  expect(rdPlans(calls)).toEqual([{ input: { hostId: "h2", regionId: 900, regionCode: "fra-x", regionName: "Frankfurt X", omitDefaultRegions: true } }])
})

test("unticking omit drops an acknowledgement that was typed", async () => {
  const calls = stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen()
  const user = userEvent.setup()
  await rdFill(user, dialog, "fra-x", "Frankfurt X")
  await user.click(within(dialog).getByRole("checkbox", { name: "Omit default regions" }))
  await user.type(within(dialog).getByLabelText("Acknowledgement"), "nodes will relay only through fra-x")
  await user.click(within(dialog).getByRole("checkbox", { name: "Omit default regions" }))
  expect(within(dialog).queryByLabelText("Acknowledgement")).toBeNull()
  await user.click(within(dialog).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Publish region fra-x" })
  expect(rdPlans(calls)).toEqual([{ input: { hostId: "h2", regionId: 900, regionCode: "fra-x", regionName: "Frankfurt X" } }])
})

test("shows the probe's warnings", async () => {
  stubFetch({ [RD_PLAN]: rdPlan() })
  const warning = "no agent other than the DERP host probed 203.0.113.10; only the controller's probe backs the region"
  const dialog = await rdOpen({ probe: { ...rdProbe, warnings: [warning] } })
  expect(within(dialog).getByText(warning)).toBeTruthy()
  expect(within(dialog).queryByText(/has not passed/)).toBeNull()
})

test("an unready probe is stated", async () => {
  stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen({ probe: { ...rdProbe, ready: false } })
  expect(within(dialog).getByText("The probe has not passed yet; the server refuses to publish the region until it does.")).toBeTruthy()
})

test("a ready probe with no warnings shows neither notice", async () => {
  stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen()
  expect(within(dialog).queryByText(/has not passed/)).toBeNull()
  expect(within(dialog).queryByRole("status")).toBeNull()
})

test("Cancel closes the dialog and plans nothing", async () => {
  const onClose = vi.fn()
  const calls = stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen({ onClose })
  const user = userEvent.setup()
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }))
  expect(onClose).toHaveBeenCalledTimes(1)
  expect(rdPlans(calls)).toEqual([])
})

test("closing the change dialog closes the flow", async () => {
  const onClose = vi.fn()
  stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen({ onClose })
  const user = userEvent.setup()
  await rdFill(user, dialog, "fra-x", "Frankfurt X")
  await user.click(within(dialog).getByRole("button", { name: "Review" }))
  const change = await screen.findByRole("dialog", { name: "Publish region fra-x" })
  await within(change).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
})

test("the form has no accessibility violations", async () => {
  stubFetch({ [RD_PLAN]: rdPlan() })
  const dialog = await rdOpen({ probe: { ...rdProbe, warnings: ["a warning"] } })
  await userEvent.setup().click(within(dialog).getByRole("checkbox", { name: "Omit default regions" }))
  expect(await axe(dialog)).toHaveNoViolations()
})
