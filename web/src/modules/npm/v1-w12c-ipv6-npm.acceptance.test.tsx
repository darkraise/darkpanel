import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { fwDocker, fwStatus } from "@/modules/firewall/fixtures"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { npInstance } from "./fixtures"
import { InstancePanel } from "./instance-panel"

const HOST = "/api/v1/hosts/h1"
const STATUS = `POST ${HOST}/actions/npm.instance.status/v1/read`
const DOCKER = `GET ${HOST}/resources/firewall/docker`
const FWSTATUS = `GET ${HOST}/resources/firewall/status`

const TODAY_ON = "The Docker firewall integration is on: ports 80 and 443 are opened for the NPM container."
const TODAY_OFF = "The Docker firewall integration is off: open ports 80 and 443 to the internet yourself, then confirm that the instance is public."

function render(routes: Record<string, StubHandler>) {
  stubFetch({
    [STATUS]: { body: { output: npInstance({ deployed: false, containerId: "", image: "", running: false, apiReady: false, public: "no", adminEmail: "" }) } },
    [`GET ${HOST}/modules/npm/availability`]: { body: { module: "npm", available: true, reason: null, actions: [] } },
    ...routes,
  })
  renderWithProviders(<InstancePanel hostId="h1" />)
}

async function typeEmail() {
  const form = await screen.findByRole("form", { name: "Deploy" })
  await userEvent.setup().type(within(form).getByLabelText("Let's Encrypt email"), "ops@example.com")
  return form
}

const unavailable = (detail: string): StubResponse => ({
  status: 503,
  body: { type: "about:blank", title: "Unavailable", status: 503, detail, code: "module.unavailable" },
})

test("A5: integration off with firewall true says the deploy allows 80 and 443 in the firewall as its last step", async () => {
  render({ [DOCKER]: { body: fwDocker({ enabled: false, supported: true }) }, [FWSTATUS]: { body: fwStatus({ supported: true }) } })
  const form = await screen.findByRole("form", { name: "Deploy" })
  const text = (await within(form).findByText(/allows ports 80 and 443 in the firewall as its last step/)).textContent ?? ""
  expect(text).toMatch(/confirm/)
  expect(text).toMatch(/public/)
  expect(text).not.toContain("yourself")
  expect(text).not.toBe(TODAY_OFF)
})

test.each([true, false])("A5: with the integration on the text is today's when the firewall status is supported %s", async (supported) => {
  render({
    [DOCKER]: { body: fwDocker({ enabled: true, supported: true }) },
    [FWSTATUS]: { body: fwStatus({ supported, unsupported: supported ? [] : ["rules"] }) },
  })
  const form = await screen.findByRole("form", { name: "Deploy" })
  await within(form).findByText(TODAY_ON)
  expect(within(form).queryByText(/as its last step/)).toBeNull()
})

test("A5: with the integration off and firewall false the text is today's", async () => {
  render({
    [DOCKER]: { body: fwDocker({ enabled: false, supported: true }) },
    [FWSTATUS]: { body: fwStatus({ supported: false, unsupported: ["rules"] }) },
  })
  const form = await screen.findByRole("form", { name: "Deploy" })
  await within(form).findByText(TODAY_OFF)
  expect(within(form).queryByText(/as its last step/)).toBeNull()
})

test("A5: Plan stays disabled until the status read succeeds", async () => {
  let release: (r: StubResponse) => void = () => undefined
  const held = new Promise<StubResponse>((resolve) => {
    release = resolve
  })
  render({ [DOCKER]: { body: fwDocker({ enabled: false, supported: true }) }, [FWSTATUS]: () => held })
  const form = await typeEmail()
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  release({ body: fwStatus({ supported: true }) })
  await within(form).findByText(/as its last step/)
  expect(plan.hasAttribute("disabled")).toBe(false)
})

test("A5: Plan stays disabled until the Docker read succeeds", async () => {
  let release: (r: StubResponse) => void = () => undefined
  const held = new Promise<StubResponse>((resolve) => {
    release = resolve
  })
  render({ [DOCKER]: () => held, [FWSTATUS]: { body: fwStatus({ supported: true }) } })
  const form = await typeEmail()
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  release({ body: fwDocker({ enabled: false, supported: true }) })
  await within(form).findByText(/as its last step/)
  expect(plan.hasAttribute("disabled")).toBe(false)
})

test("A5: a failed status read shows its error and keeps Plan disabled", async () => {
  render({ [DOCKER]: { body: fwDocker({ enabled: false, supported: true }) }, [FWSTATUS]: unavailable("the firewall status cannot be read") })
  const form = await typeEmail()
  expect((await within(form).findByRole("alert")).textContent).toBe("the firewall status cannot be read")
  expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
})
