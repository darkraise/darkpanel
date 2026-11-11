import { screen, waitFor } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import type { Host, ModuleState } from "@/api/types"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { HostPage } from "./host"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const SUMMARY = "GET /api/v1/hosts/h1/resources/hostinfo/summary"

const summary: Summary = {
  hostname: "web-1", os: { id: "debian", versionId: "13", prettyName: "Debian GNU/Linux 13 (trixie)" }, kernel: "6.12.0",
  arch: "amd64", uptimeSeconds: 60, bootTime: "2026-10-19T08:00:00Z", cpu: { model: "QEMU", logicalCount: 1 },
  memory: { totalBytes: 1073741824, availableBytes: 536870912 }, swap: { totalBytes: 0, freeBytes: 0 },
  load: { one: 0, five: 0, fifteen: 0 }, rootFs: { totalBytes: 1073741824, availableBytes: 536870912 }, darkpanelVersion: "0.1.0",
}

function state(id: string, enabled: boolean, available: boolean, reason: string | null = null): ModuleState {
  return { id, version: 1, enabled, available, reason, checkedAt: null }
}

function host(modules: ModuleState[]): Host {
  return { id: "h1", name: "web-1", isLocal: true, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules, ingestion: null }
}

function renderHost(path: string, list: StubResponse | (() => Promise<StubResponse>)) {
  stubFetch({ [HOSTS]: list, [SUMMARY]: { body: summary } })
  return renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId" element={<HostPage />} />
      <Route path="/hosts/:hostId/:moduleId/*" element={<HostPage />} />
    </Routes>,
    { path },
  )
}

const withModules = (modules: ModuleState[]): StubResponse => ({ body: { output: { hosts: [host(modules)] } } })

test("the host page passes axe with the module's own page", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", true, true)]))
  await screen.findByText("Debian GNU/Linux 13 (trixie)")
  await expectNoAxeViolations(container)
})

test("the host page passes axe while the host list loads", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", () => new Promise<StubResponse>(() => undefined))
  await screen.findByLabelText("Loading the host")
  await expectNoAxeViolations(container)
})

test("the host page passes axe when the host list fails", async () => {
  const problem = { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" }
  const { container } = renderHost("/hosts/h1/hostinfo", { status: 503, body: problem })
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for an unknown host", async () => {
  const { container } = renderHost("/hosts/nope/hostinfo", withModules([state("hostinfo", true, true)]))
  await screen.findByText("Host not found")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for a host without modules", async () => {
  const { container } = renderHost("/hosts/h1", withModules([state("hostinfo", false, true)]))
  await screen.findByText("No modules are enabled on this host.")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for a module that is not enabled", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", false, true)]))
  await screen.findByText("This module is not enabled on this host.")
  await expectNoAxeViolations(container)
})

test("the host page passes axe for an unavailable module", async () => {
  const { container } = renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", true, false, "/proc is not mounted")]))
  await screen.findByText("/proc is not mounted")
  await expectNoAxeViolations(container)
})

test("the host page names the host in its heading and marks the current tab", async () => {
  renderHost("/hosts/h1/hostinfo", withModules([state("hostinfo", true, true)]))
  const tab = await screen.findByRole("link", { name: "Host information" })
  expect(tab.getAttribute("aria-current")).toBe("page")
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["web-1"])
})

test.each([
  ["a module page", "/hosts/h1/hostinfo", withModules([state("hostinfo", true, true)]), "web-1 · Host information · Darkpanel"],
  ["an unavailable module", "/hosts/h1/hostinfo", withModules([state("hostinfo", true, false, "/proc is not mounted")]), "web-1 · Host information · Darkpanel"],
  ["a module that is not enabled", "/hosts/h1/hostinfo", withModules([state("hostinfo", false, true)]), "web-1 · Host information · Darkpanel"],
  ["a host without modules", "/hosts/h1", withModules([state("hostinfo", false, true)]), "web-1 · Darkpanel"],
  ["an unknown host", "/hosts/nope/hostinfo", withModules([state("hostinfo", true, true)]), "Host not found · Darkpanel"],
])("the page title of %s", async (_name, path, list, title) => {
  renderHost(path, list)
  await waitFor(() => expect(document.title).toBe(title))
})

test("the page title is Host while the host list loads", async () => {
  renderHost("/hosts/h1/hostinfo", () => new Promise<StubResponse>(() => undefined))
  await screen.findByLabelText("Loading the host")
  expect(document.title).toBe("Host · Darkpanel")
})
