import { act, screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Link, Route, Routes, useParams } from "react-router"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { fwDocker } from "@/modules/firewall/fixtures"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { npAccessList, npCertificate, npInstance, npProxyHost } from "./fixtures"
import NpmRoutes from "./routes"
import type { InstanceStatus } from "./types"

const PG_STATUS = "POST /api/v1/hosts/h1/actions/npm.instance.status/v1/read"
const PG_EXPOSED = "The NPM admin interface is published at https://npm.example.com. Remove the exposure when you no longer need it."

function pgStatus(over: Partial<InstanceStatus> = {}): InstanceStatus {
  return npInstance({ deployed: true, containerId: "c0ffee", running: true, apiReady: true, public: "confirmed", adminEmail: "admin@example.com", ...over })
}

function pgRender(path: string, status: InstanceStatus, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [PG_STATUS]: { body: { output: status } },
    "GET /api/v1/hosts/h1/resources/firewall/docker": { body: fwDocker() },
    ...routes,
  })
  const result = renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId/npm/*" element={<NpmRoutes hostId="h1" />} />
    </Routes>,
    { path },
  )
  return { calls, ...result }
}

test("opens on the Instance tab and reads the status once for the page and the panel", async () => {
  const { calls } = pgRender("/hosts/h1/npm", pgStatus())
  expect((await screen.findByText("admin@example.com")).tagName).toBe("DD")
  expect(screen.getByRole("tab", { name: "Instance", selected: true })).toBeTruthy()
  expect(calls.filter((c) => c.method === "POST" && c.path.endsWith("/npm.instance.status/v1/read"))).toHaveLength(1)
})

test("warns while the admin interface is exposed", async () => {
  pgRender("/hosts/h1/npm", pgStatus({ adminExposure: { hostname: "npm.example.com", proxyHostId: 4, certificateId: 5 } }))
  expect((await screen.findByText(PG_EXPOSED)).textContent).toBe(PG_EXPOSED)
})

test("shows no warning when the admin interface is not exposed", async () => {
  pgRender("/hosts/h1/npm", pgStatus())
  await screen.findByText("admin@example.com")
  expect(screen.queryByText(/is published at/)).toBeNull()
})

test("an unknown subpage is answered", () => {
  pgRender("/hosts/h1/npm/nope", pgStatus())
  expect(screen.getByText("Page not found").textContent).toBe("Page not found")
})

test("the page has no accessibility violations", async () => {
  const { container } = pgRender("/hosts/h1/npm", pgStatus({ adminExposure: { hostname: "npm.example.com", proxyHostId: 4, certificateId: 5 } }))
  await screen.findByText(PG_EXPOSED)
  expect(await axe(container)).toHaveNoViolations()
})

const PG_LISTS = {
  "POST /api/v1/hosts/h1/actions/npm.proxyhost.list/v1/read": { body: { output: { items: [npProxyHost({ id: 3, domains: ["app.example.com"], reserved: "", unclaimed: false })] } } },
  "POST /api/v1/hosts/h1/actions/npm.accesslist.list/v1/read": { body: { output: { items: [npAccessList({ id: 2, name: "darkpanel-01jd-1", reserved: "", unclaimed: false })] } } },
  "POST /api/v1/hosts/h1/actions/npm.certificate.list/v1/read": { body: { output: { items: [npCertificate({ id: 5, name: "darkpanel-01jd-3", reserved: "", unclaimed: false })] } } },
}

test("the page has the Instance, Proxy hosts, Access lists and Certificates tabs", async () => {
  pgRender("/hosts/h1/npm", pgStatus(), PG_LISTS)
  await screen.findByText("admin@example.com")
  expect(within(screen.getByRole("tablist")).getAllByRole("tab").map((t) => t.textContent)).toEqual(["Instance", "Proxy hosts", "Access lists", "Certificates"])
})

test("each list tab reads its list only when opened", async () => {
  const { calls } = pgRender("/hosts/h1/npm", pgStatus(), PG_LISTS)
  const user = userEvent.setup()
  await screen.findByText("admin@example.com")
  const reads = (action: string) => calls.filter((c) => c.method === "POST" && c.path === `/api/v1/hosts/h1/actions/${action}/v1/read`).length
  expect([reads("npm.proxyhost.list"), reads("npm.accesslist.list"), reads("npm.certificate.list")]).toEqual([0, 0, 0])
  for (const [tab, table, action] of [
    ["Proxy hosts", "Proxy hosts", "npm.proxyhost.list"],
    ["Access lists", "Access lists", "npm.accesslist.list"],
    ["Certificates", "Certificates", "npm.certificate.list"],
  ] as const) {
    await user.click(screen.getByRole("tab", { name: tab }))
    await screen.findByRole("table", { name: table })
    await waitFor(() => expect(reads(action)).toBe(1))
  }
})

function PgHostRoutes() {
  const { hostId = "" } = useParams()
  return <NpmRoutes hostId={hostId} />
}

test("drops an admin password still being revealed for the previous host", async () => {
  let answer: (response: StubResponse) => void = () => undefined
  stubFetch({
    [PG_STATUS]: { body: { output: pgStatus() } },
    "POST /api/v1/hosts/h2/actions/npm.instance.status/v1/read": { body: { output: pgStatus({ adminEmail: "other@example.com" }) } },
    "POST /api/v1/hosts/h1/actions/npm.admin.reveal-password/v1/reveal": () =>
      new Promise<StubResponse>((resolve) => {
        answer = resolve
      }),
  })
  renderWithProviders(
    <>
      <Link to="/hosts/h2/npm">Other host</Link>
      <Routes>
        <Route path="/hosts/:hostId/npm/*" element={<PgHostRoutes />} />
      </Routes>
    </>,
    { path: "/hosts/h1/npm" },
  )
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Reveal admin password" }))
  await user.click(screen.getByRole("link", { name: "Other host" }))
  expect((await screen.findByText("other@example.com")).tagName).toBe("DD")
  await act(async () => {
    answer({ body: { value: { email: "admin@example.com", password: "s3cret-Pa55" } } })
    await new Promise((resolve) => setTimeout(resolve, 50))
  })
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(screen.queryByText("s3cret-Pa55")).toBeNull()
  expect(screen.getByRole("button", { name: "Reveal admin password" }).hasAttribute("disabled")).toBe(false)
})
