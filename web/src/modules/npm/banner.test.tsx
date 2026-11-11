import { useState, type ReactElement } from "react"
import { fireEvent, screen, waitFor } from "@testing-library/react"
import type { QueryClient } from "@tanstack/react-query"
import { expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import type { Host } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { AdminExposureBanner } from "./banner"
import { npInstance } from "./fixtures"
import { npmFleetKey, type HostInstance } from "./types"

const BN_LIST = "POST /api/v1/actions/npm.instance.list/v1/read"
const BN_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const BN_WEB1 = "The NPM admin interface of web-1 is exposed at https://npm.example.com."
const BN_UNCHECKED = "Could not check whether the NPM admin UI is exposed."

function bnExposed(hostId: string, hostName: string, hostname: string): HostInstance {
  return { hostId, hostName, status: npInstance({ deployed: true, adminExposure: { hostname, proxyHostId: 4, certificateId: 5 } }) }
}

function bnPlain(hostId: string, hostName: string): HostInstance {
  return { hostId, hostName, status: npInstance({ deployed: true }) }
}

function bnFleet(instances: HostInstance[], unreadable: string[] = []) {
  return { body: { output: { instances, unreadable } } }
}

function bnHost(id: string, name: string): Host {
  return { id, name, isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules: [], ingestion: null }
}

function bnHosts(...hosts: Host[]) {
  return { body: { output: { hosts } } }
}

function bnRender(response: StubHandler, ui: ReactElement = <AdminExposureBanner />, extra: Record<string, StubHandler> = {}) {
  const calls = stubFetch({ [BN_LIST]: response, ...extra })
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
  bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com"), bnPlain("h2", "web-2"), bnExposed("h3", "web-3", "admin.example.org")]))
  expect(await screen.findByText(BN_WEB1)).toBeTruthy()
  expect(screen.getByText("The NPM admin interface of web-3 is exposed at https://admin.example.org.")).toBeTruthy()
  expect(screen.queryByText(/web-2/)).toBeNull()
  expect(screen.getByRole("link", { name: "Open Nginx Proxy Manager on web-1" }).getAttribute("href")).toBe("/hosts/h1/npm")
  expect(screen.getByRole("link", { name: "Open Nginx Proxy Manager on web-3" }).getAttribute("href")).toBe("/hosts/h3/npm")
})

test("renders nothing when no host exposes its admin interface and every host could be read", async () => {
  const { client, calls } = bnRender(bnFleet([bnPlain("h1", "web-1")]))
  await bnSettled(client)
  expect(screen.queryByText(/NPM admin interface/)).toBeNull()
  expect(screen.queryByText(/Could not check/)).toBeNull()
  expect(screen.queryByRole("link")).toBeNull()
  expect(calls.some((c) => c.path === "/api/v1/actions/core.host.list/v1/read")).toBe(false)
})

test("warns about the hosts whose status could not be read, by display name, beside the exposure warnings", async () => {
  bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com")], ["h2", "h3"]), <AdminExposureBanner />, {
    [BN_HOSTS]: bnHosts(bnHost("h1", "web-1"), bnHost("h2", "web-2"), bnHost("h3", "web-3")),
  })
  expect(await screen.findByText("Could not check whether the NPM admin UI is exposed on: web-2, web-3.")).toBeTruthy()
  expect(screen.getByText(BN_WEB1)).toBeTruthy()
  expect(screen.getAllByText(/NPM admin/)).toHaveLength(2)
})

test("names an unreadable host by its id while the host list is not available", async () => {
  bnRender(bnFleet([], ["h2"]), <AdminExposureBanner />, {
    [BN_HOSTS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } },
  })
  expect(await screen.findByText("Could not check whether the NPM admin UI is exposed on: h2.")).toBeTruthy()
})

test("warns that the check failed when the fleet read fails", async () => {
  const { client } = bnRender({ status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } })
  expect(await screen.findByText(BN_UNCHECKED)).toBeTruthy()
  expect(client.getQueryState(npmFleetKey)?.status).toBe("error")
  expect(screen.queryByText(/is exposed at/)).toBeNull()
  expect(screen.queryByRole("alert")).toBeNull()
})

test("warns that the check failed when the controller answers with something that is not an instance list", async () => {
  const { client } = bnRender({ body: { output: { instances: "none", unreadable: [] } } })
  expect(await screen.findByText(BN_UNCHECKED)).toBeTruthy()
  expect(client.getQueryState(npmFleetKey)?.status).toBe("error")
})

test("warns that the check failed when the answer lacks the unreadable hosts", async () => {
  bnRender({ body: { output: { instances: [bnExposed("h1", "web-1", "npm.example.com")] } } })
  expect(await screen.findByText(BN_UNCHECKED)).toBeTruthy()
  expect(screen.queryByText(BN_WEB1)).toBeNull()
})

test("the fleet read is kept for 60 seconds before a new mount reads it again", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const { calls } = bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com")]), <BnToggle />)
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
  const { container } = bnRender(bnFleet([bnExposed("h1", "web-1", "npm.example.com")], ["h2"]), <AdminExposureBanner />, {
    [BN_HOSTS]: bnHosts(bnHost("h2", "web-2")),
  })
  await screen.findByText(BN_WEB1)
  await screen.findByText("Could not check whether the NPM admin UI is exposed on: web-2.")
  expect(await axe(container)).toHaveNoViolations()
})
