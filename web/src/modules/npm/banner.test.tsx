import { useState, type ReactElement } from "react"
import { fireEvent, screen, waitFor } from "@testing-library/react"
import type { QueryClient } from "@tanstack/react-query"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AdminExposureBanner } from "./banner"
import { npInstance } from "./fixtures"
import { npmFleetKey, type HostInstance } from "./types"

const BN_LIST = "POST /api/v1/actions/npm.instance.list/v1/read"
const BN_WEB1 = "The NPM admin interface of web-1 is exposed at https://npm.example.com."

function bnExposed(hostId: string, hostName: string, hostname: string): HostInstance {
  return { hostId, hostName, status: npInstance({ deployed: true, adminExposure: { hostname, proxyHostId: 4, certificateId: 5 } }) }
}

function bnPlain(hostId: string, hostName: string): HostInstance {
  return { hostId, hostName, status: npInstance({ deployed: true }) }
}

function bnRender(response: StubHandler, ui: ReactElement = <AdminExposureBanner />) {
  const calls = stubFetch({ [BN_LIST]: response })
  const result = renderWithProviders(ui)
  return { calls, ...result }
}

function bnReads(calls: FetchCall[]): number {
  return calls.filter((c) => c.method === "POST" && c.path === "/api/v1/actions/npm.instance.list/v1/read").length
}

async function bnSettled(client: QueryClient) {
  await waitFor(() => expect(["success", "error"]).toContain(client.getQueryState(npmFleetKey)?.status))
}

function BnToggle() {
  const [shown, setShown] = useState(true)
  return (
    <>
      <button onClick={() => setShown((s) => !s)}>Toggle</button>
      {shown && <AdminExposureBanner />}
    </>
  )
}

test("shows one warning for each host whose admin interface is exposed, linking to its NPM page", async () => {
  const output = { instances: [bnExposed("h1", "web-1", "npm.example.com"), bnPlain("h2", "web-2"), bnExposed("h3", "web-3", "admin.example.org")] }
  bnRender({ body: { output } })
  expect(await screen.findByText(BN_WEB1)).toBeTruthy()
  expect(screen.getByText("The NPM admin interface of web-3 is exposed at https://admin.example.org.")).toBeTruthy()
  expect(screen.queryByText(/web-2/)).toBeNull()
  expect(screen.getByRole("link", { name: "Open Nginx Proxy Manager on web-1" }).getAttribute("href")).toBe("/hosts/h1/npm")
  expect(screen.getByRole("link", { name: "Open Nginx Proxy Manager on web-3" }).getAttribute("href")).toBe("/hosts/h3/npm")
})

test("renders nothing when no host exposes its admin interface", async () => {
  const { client } = bnRender({ body: { output: { instances: [bnPlain("h1", "web-1")] } } })
  await bnSettled(client)
  expect(screen.queryByText(/NPM admin interface/)).toBeNull()
  expect(screen.queryByRole("link")).toBeNull()
})

test("renders nothing when the fleet read fails", async () => {
  const { client } = bnRender({ status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } })
  await bnSettled(client)
  expect(client.getQueryState(npmFleetKey)?.status).toBe("error")
  expect(screen.queryByText(/NPM admin interface/)).toBeNull()
  expect(screen.queryByRole("alert")).toBeNull()
})

test("renders nothing when the controller answers with something that is not an instance list", async () => {
  const { client } = bnRender({ body: { output: { instances: "none" } } })
  await bnSettled(client)
  expect(client.getQueryState(npmFleetKey)?.status).toBe("error")
  expect(screen.queryByText(/NPM admin interface/)).toBeNull()
})

test("the fleet read is kept for 60 seconds before a new mount reads it again", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const { calls } = bnRender({ body: { output: { instances: [bnExposed("h1", "web-1", "npm.example.com")] } } }, <BnToggle />)
    expect(await screen.findByText(BN_WEB1)).toBeTruthy()
    const toggle = () => fireEvent.click(screen.getByRole("button", { name: "Toggle" }))
    toggle()
    expect(screen.queryByText(BN_WEB1)).toBeNull()
    await vi.advanceTimersByTimeAsync(30_000)
    toggle()
    expect(await screen.findByText(BN_WEB1)).toBeTruthy()
    expect(bnReads(calls)).toBe(1)
    toggle()
    await vi.advanceTimersByTimeAsync(31_000)
    toggle()
    await waitFor(() => expect(bnReads(calls)).toBe(2))
  } finally {
    vi.useRealTimers()
  }
})

test("the banner has no accessibility violations", async () => {
  const { container } = bnRender({ body: { output: { instances: [bnExposed("h1", "web-1", "npm.example.com")] } } })
  await screen.findByText(BN_WEB1)
  expect(await axe(container)).toHaveNoViolations()
})
