import { lazy } from "react"
import { screen } from "@testing-library/react"
import { Server } from "lucide-react"
import { Route, Routes } from "react-router"
import { describe, expect, test } from "vitest"
import type { Host, ModuleState } from "@/api/types"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { enabledModules, globalPages, hostCreateExtensions, moduleRegistry, moduleView } from "@/modules/registry"
import type { ModuleEntry } from "@/modules/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { HostPage } from "./host"

function state(id: string, enabled: boolean, available: boolean, reason: string | null = null): ModuleState {
  return { id, version: 1, enabled, available, reason, checkedAt: null }
}

function host(modules: ModuleState[]): Host {
  return { id: "h1", name: "web-1", isLocal: true, online: true, modules, ingestion: null }
}

function entry(id: string): ModuleEntry {
  return { id, label: id.toUpperCase(), icon: Server, routes: lazy(async () => ({ default: () => null })) }
}

const summary: Summary = {
  hostname: "web-1", os: { id: "debian", versionId: "13", prettyName: "Debian GNU/Linux 13 (trixie)" }, kernel: "6.12.0",
  arch: "amd64", uptimeSeconds: 60, bootTime: "2026-10-19T08:00:00Z", cpu: { model: "QEMU", logicalCount: 1 },
  memory: { totalBytes: 1073741824, availableBytes: 536870912 }, swap: { totalBytes: 0, freeBytes: 0 },
  load: { one: 0, five: 0, fifteen: 0 }, rootFs: { totalBytes: 1073741824, availableBytes: 536870912 }, darkpanelVersion: "0.1.0",
}

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const SUMMARY = "GET /api/v1/hosts/h1/resources/hostinfo/summary"

function renderHost(path: string, modules: ModuleState[]) {
  const calls = stubFetch({ [HOSTS]: { body: { output: { hosts: [host(modules)] } } }, [SUMMARY]: { body: summary } })
  renderWithProviders(
    <Routes>
      <Route path="/hosts/:hostId" element={<HostPage />} />
      <Route path="/hosts/:hostId/:moduleId/*" element={<HostPage />} />
    </Routes>,
    { path },
  )
  return calls
}

describe("registry", () => {
  // Adding a module must not require editing this test (R14).
  test("registers the host-information module", () => {
    expect(moduleRegistry.find((m) => m.id === "hostinfo")?.label).toBe("Host information")
    expect(new Set(moduleRegistry.map((m) => m.id)).size).toBe(moduleRegistry.length)
  })

  test("tabs follow the registry order and only enabled modules", () => {
    const entries = [entry("alpha"), entry("beta"), entry("gamma"), entry("delta")]
    const h = host([state("gamma", false, true), state("beta", true, false, "docker is not installed"), state("alpha", true, true)])
    expect(enabledModules(entries, h).map((m) => m.id)).toEqual(["alpha", "beta"])
  })

  test.each([
    ["alpha", "pages", undefined],
    ["beta", "unavailable", "docker is not installed"],
    ["gamma", "not-enabled", undefined],
    ["delta", "not-enabled", undefined],
    ["omega", "not-enabled", undefined],
  ])("moduleView for %s is %s", (id, kind, reason) => {
    const entries = [entry("alpha"), entry("beta"), entry("gamma"), entry("delta")]
    const h = host([state("alpha", true, true), state("beta", true, false, "docker is not installed"), state("gamma", false, true)])
    const view = moduleView(entries, h, id)
    expect(view.kind).toBe(kind)
    expect(view.kind === "unavailable" ? view.reason : undefined).toBe(reason)
  })

  test("an entry with only a global page has no host tab and no host view", () => {
    const tailnet: ModuleEntry = {
      id: "tailnet", label: "Tailnet", icon: Server,
      global: { path: "tailnet", label: "Tailnet", routes: lazy(async () => ({ default: () => null })) },
    }
    const h = host([state("alpha", true, true), state("tailnet", true, true)])
    expect(enabledModules([entry("alpha"), tailnet], h).map((m) => m.id)).toEqual(["alpha"])
    expect(moduleView([entry("alpha"), tailnet], h, "tailnet").kind).toBe("not-enabled")
  })

  test("globalPages lists every global page in registry order", () => {
    const page = (path: string) => ({ path, label: path, routes: lazy(async () => ({ default: () => null })) })
    const entries: ModuleEntry[] = [
      { ...entry("alpha"), global: page("alpha") },
      entry("beta"),
      { id: "gamma", label: "Gamma", icon: Server, global: page("gamma") },
    ]
    expect(globalPages(entries).map((p) => p.path)).toEqual(["alpha", "gamma"])
  })

  test("hostCreateExtensions lists the extensions of modules enabled on the host", () => {
    const ext = (id: string) => ({ id, render: lazy(async () => ({ default: () => null })) })
    const entries: ModuleEntry[] = [
      { ...entry("alpha"), hostCreate: ext("alpha.join") },
      { ...entry("beta"), hostCreate: ext("beta.join") },
      entry("gamma"),
      { id: "delta", label: "Delta", icon: Server, hostCreate: ext("delta.join") },
    ]
    const h = host([state("alpha", true, true), state("beta", false, true), state("gamma", true, true), state("delta", true, false)])
    expect(hostCreateExtensions(entries, h).map((e) => e.id)).toEqual(["alpha.join", "delta.join"])
  })

  test("an unavailable module without a reason gets a default one", () => {
    const view = moduleView([entry("beta")], host([state("beta", true, false)]), "beta")
    expect(view.kind === "unavailable" ? view.reason : "").toBe("The module is unavailable on this host.")
  })
})

describe("host page", () => {
  test("builds the tabs from the registry and renders the module's pages", async () => {
    renderHost("/hosts/h1/hostinfo", [state("hostinfo", true, true)])
    const tab = await screen.findByRole("link", { name: "Host information" })
    expect(tab.getAttribute("href")).toBe("/hosts/h1/hostinfo")
    expect(tab.getAttribute("aria-current")).toBe("page")
    expect((await screen.findByText("Debian GNU/Linux 13 (trixie)")).textContent).toBe("Debian GNU/Linux 13 (trixie)")
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("web-1")
  })

  test("an enabled but unavailable module shows its tab with the reason", async () => {
    const calls = renderHost("/hosts/h1/hostinfo", [state("hostinfo", true, false, "/proc is not mounted")])
    expect((await screen.findByText("/proc is not mounted")).textContent).toBe("/proc is not mounted")
    expect(screen.getByText("Host information is unavailable").textContent).toBe("Host information is unavailable")
    expect(screen.getByRole("link", { name: "Host information" })).toBeTruthy()
    expect(calls.some((c) => c.path.includes("/resources/"))).toBe(false)
  })

  test("a disabled module has no tab", async () => {
    renderHost("/hosts/h1/hostinfo", [state("hostinfo", false, true)])
    expect((await screen.findByText("This module is not enabled on this host.")).textContent).toBe("This module is not enabled on this host.")
    expect(screen.queryByRole("link", { name: "Host information" })).toBeNull()
  })

  test("the host index opens the first enabled module", async () => {
    renderHost("/hosts/h1", [state("hostinfo", true, true)])
    expect((await screen.findByText("Debian GNU/Linux 13 (trixie)")).textContent).toBe("Debian GNU/Linux 13 (trixie)")
    expect(screen.getByTestId("location").textContent).toBe("/hosts/h1/hostinfo")
  })

  test("the host index says so when no module is enabled", async () => {
    renderHost("/hosts/h1", [state("hostinfo", false, true)])
    expect((await screen.findByText("No modules are enabled on this host.")).textContent).toBe("No modules are enabled on this host.")
  })

  test("an unknown host is reported", async () => {
    renderHost("/hosts/nope/hostinfo", [state("hostinfo", true, true)])
    expect((await screen.findByText("Host not found")).textContent).toBe("Host not found")
  })
})
