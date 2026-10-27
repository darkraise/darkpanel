import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { AddRule } from "./rule-form"

const RF_PLAN = "POST /api/v1/hosts/h1/actions/firewall.rule.add/v1/plan"

function rfRender() {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": {
      body: { module: "firewall", available: true, reason: null, actions: [{ name: "firewall.rule.add", available: true, reason: null }] },
    },
    [RF_PLAN]: {
      body: { planId: "p1", hostId: "h1", action: "firewall.rule.add", version: 1, inputHash: "h", diff: [{ op: "+", text: "allow in tcp to any port 443" }], guards: [], expiresAt: "2026-10-24T09:00:00Z", confirmation: null },
    },
  })
  renderWithProviders(<AddRule hostId="h1" />)
  return calls
}

async function rfOpen() {
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Add rule" }))
  const form = await screen.findByRole("form", { name: "Rule" })
  return { user, form }
}

test("plans firewall.rule.add with the rule the form describes", async () => {
  const calls = rfRender()
  const { user, form } = await rfOpen()
  await user.type(within(form).getByLabelText("To port"), "443")
  await user.type(within(form).getByLabelText("Comment"), "web")
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  const dialog = await screen.findByRole("dialog", { name: "Add a rule" })
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ allow in tcp to any port 443")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({
    input: { rule: { action: "allow", direction: "in", proto: "tcp", from: "any", to: "any", toPort: "443", family: "both", comment: "web" } },
  })
})

test("a routed rule carries both interfaces, its ports, family and logging", async () => {
  const calls = rfRender()
  const { user, form } = await rfOpen()
  await user.click(within(form).getByLabelText("deny"))
  await user.click(within(form).getByLabelText("route"))
  await user.click(within(form).getByLabelText("udp"))
  await user.clear(within(form).getByLabelText("From"))
  await user.type(within(form).getByLabelText("From"), "10.0.0.0/8")
  await user.type(within(form).getByLabelText("From port"), "1000:2000")
  await user.clear(within(form).getByLabelText("To"))
  await user.type(within(form).getByLabelText("To"), "172.17.0.2")
  await user.type(within(form).getByLabelText("To port"), "53")
  await user.type(within(form).getByLabelText("In on interface"), "eth0")
  await user.type(within(form).getByLabelText("Out on interface"), "docker0")
  await user.click(within(form).getByLabelText("v4"))
  await user.click(within(form).getByLabelText("log-all"))
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog", { name: "Add a rule" })).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({
    input: {
      rule: {
        action: "deny", direction: "route", proto: "udp", from: "10.0.0.0/8", fromPort: "1000:2000", to: "172.17.0.2", toPort: "53",
        inIface: "eth0", outIface: "docker0", family: "v4", log: "log-all",
      },
    },
  })
})

test("fields that do not apply are disabled and left out", async () => {
  const calls = rfRender()
  const { user, form } = await rfOpen()
  expect(within(form).getByLabelText("Out on interface").hasAttribute("disabled")).toBe(true)
  await user.click(within(form).getByLabelText("limit"))
  await user.click(within(form).getByLabelText("out"))
  expect(within(form).getByLabelText("In on interface").hasAttribute("disabled")).toBe(true)
  expect(within(form).getByLabelText("limit").hasAttribute("disabled")).toBe(true)
  await user.click(within(form).getByLabelText("esp"))
  expect(within(form).getByLabelText("To port").hasAttribute("disabled")).toBe(true)
  await user.click(within(form).getByRole("button", { name: "Plan" }))
  await within(await screen.findByRole("dialog", { name: "Add a rule" })).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({
    input: { rule: { action: "allow", direction: "out", proto: "esp", from: "any", to: "any", family: "both" } },
  })
})

test("the rule form has no accessibility violations", async () => {
  rfRender()
  const { form } = await rfOpen()
  expect(await axe(form)).toHaveNoViolations()
})
