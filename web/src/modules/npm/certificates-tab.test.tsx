import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { CertificatesTab } from "./certificates-tab"
import { npCertificate } from "./fixtures"
import type { CertificateView } from "./types"

const CE_HOST = "/api/v1/hosts/h1"
const CE_LIST = `POST ${CE_HOST}/actions/npm.certificate.list/v1/read`

function cePlan(call: FetchCall) {
  const parts = call.path.split("/")
  return {
    body: {
      planId: "p1", hostId: "h1", action: parts[parts.length - 3] ?? "", version: 1, inputHash: "h",
      diff: [{ op: "~", text: "change" }], guards: [], expiresAt: "2026-11-02T13:00:00Z", confirmation: null,
    },
  }
}

function ceCerts(): CertificateView[] {
  return [
    npCertificate({ id: 5, name: "darkpanel-01jd-3", domains: ["app.example.com", "www.example.com"], expiresOn: "2027-01-30 10:00:00", reserved: "", unclaimed: false }),
    npCertificate({ id: 6, name: "darkpanel-01jd-4", domains: ["npm.example.com"], expiresOn: "2027-02-01 10:00:00", reserved: "npm.admin.expose", unclaimed: false }),
    npCertificate({ id: 7, name: "darkpanel-01jd-8", domains: ["old.example.com"], expiresOn: "2027-02-02 10:00:00", reserved: "", unclaimed: true }),
  ]
}

function ceRender(items: CertificateView[] = ceCerts(), routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [CE_LIST]: { body: { output: { items } } },
    [`GET ${CE_HOST}/modules/npm/availability`]: { body: { module: "npm", available: true, reason: null, actions: [] } },
    [`POST ${CE_HOST}/actions/npm.certificate.request/v1/plan`]: cePlan,
    [`POST ${CE_HOST}/actions/npm.certificate.remove/v1/plan`]: cePlan,
    ...routes,
  })
  const result = renderWithProviders(<CertificatesTab hostId="h1" />)
  return { calls, ...result }
}

function ceCells(n: number): (string | null)[] {
  const row = within(screen.getByRole("table", { name: "Certificates" })).getAllByRole("row")[n]
  if (row === undefined) throw new Error(`no row ${n}`)
  return within(row).getAllByRole("cell").map((c) => c.textContent)
}

function cePlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

test("lists each certificate with its domains, expiry and marks", async () => {
  ceRender()
  await screen.findByRole("table", { name: "Certificates" })
  expect(ceCells(1).slice(0, 5)).toEqual(["5", "darkpanel-01jd-3", "app.example.com, www.example.com", "2027-01-30 10:00:00", ""])
  expect(ceCells(2).slice(0, 5)).toEqual(["6", "darkpanel-01jd-4", "npm.example.com", "2027-02-01 10:00:00", "Reserved: npm.admin.expose"])
  expect(ceCells(3).slice(0, 5)).toEqual(["7", "darkpanel-01jd-8", "old.example.com", "2027-02-02 10:00:00", "Unclaimed Darkpanel object"])
})

test("says so when there are no certificates", async () => {
  ceRender([])
  expect((await screen.findByText("No certificates")).textContent).toBe("No certificates")
})

test("shows the reason when the certificates cannot be read", async () => {
  ceRender([], {
    [CE_LIST]: { status: 409, body: { type: "about:blank", title: "Unhealthy", status: 409, detail: "Nginx Proxy Manager is not running", code: "npm.unhealthy" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("Nginx Proxy Manager is not running")
})

test("a reserved certificate cannot be removed here", async () => {
  ceRender()
  await screen.findByRole("table", { name: "Certificates" })
  expect(screen.getByRole("button", { name: "Remove darkpanel-01jd-4" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Remove darkpanel-01jd-3" }).hasAttribute("disabled")).toBe(false)
})

test("removing a certificate plans npm.certificate.remove with its id", async () => {
  const { calls } = ceRender()
  await screen.findByRole("table", { name: "Certificates" })
  await userEvent.setup().click(screen.getByRole("button", { name: "Remove darkpanel-01jd-8" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove certificate darkpanel-01jd-8" })
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe(`${CE_HOST}/actions/npm.certificate.remove/v1/plan`)
  expect(cePlanBodies(calls)).toEqual([{ input: { id: 7 } }])
})

test("the request form needs a domain and plans npm.certificate.request with the domains", async () => {
  const { calls } = ceRender()
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Request certificate" }))
  const form = await screen.findByRole("form", { name: "Certificate" })
  const plan = within(form).getByRole("button", { name: "Plan" })
  expect(plan.hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Domains"), "A.example.com b.example.com")
  expect(plan.hasAttribute("disabled")).toBe(false)
  await user.click(plan)
  const dialog = await screen.findByRole("dialog", { name: "Request a certificate for a.example.com, b.example.com" })
  await within(dialog).findByLabelText("Planned changes")
  expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe(`${CE_HOST}/actions/npm.certificate.request/v1/plan`)
  expect(cePlanBodies(calls)).toEqual([{ input: { domains: ["a.example.com", "b.example.com"] } }])
})

test("the table has no accessibility violations", async () => {
  ceRender()
  const table = await screen.findByRole("table", { name: "Certificates" })
  expect(await axe(table)).toHaveNoViolations()
})
