import { Fragment, Suspense, type ComponentType } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { describe, expect, test } from "vitest"
import type { Host, Scope, Session } from "@/api/types"
import { HostPage } from "@/pages/host"
import { adminSession, makeHost, memberSession, renderWithProviders, stubFetch } from "@/test/utils"
import { hostTabs, moduleRegistry, moduleView } from "./registry"
import type { ContainerRef } from "./types"

const regContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

// regComponents lists every lazy component of the registry, named.
function regComponents(): Array<[string, ComponentType]> {
  const out: Array<[string, ComponentType]> = []
  for (const entry of moduleRegistry) {
    const { routes: Routes, global, hostCreate } = entry
    if (Routes !== undefined) out.push([`${entry.id} routes`, () => <Routes hostId="h1" />])
    if (global !== undefined) out.push([`${entry.id} global page`, () => <global.routes />])
    if (hostCreate !== undefined) out.push([`${entry.id} host-create`, () => <hostCreate.render value={{}} onChange={() => {}} />])
    for (const action of entry.containerActions ?? []) {
      out.push([`${action.id} container action`, () => <action.render container={regContainer} onDone={() => {}} />])
    }
  }
  return out
}

describe("module registry", () => {
  // One Suspense boundary for all of them: each boundary waits out React's
  // fallback throttle once.
  test("every lazy component of the registry loads", async () => {
    stubFetch({})
    renderWithProviders(
      <Suspense fallback={<p>Loading the components</p>}>
        {regComponents().map(([name, Component]) => (
          <Fragment key={name}>
            <Component />
          </Fragment>
        ))}
      </Suspense>,
    )
    // Every module chunk loads lazily, which takes seconds on a slow CI runner.
    await waitFor(() => expect(screen.queryByText("Loading the components")).toBeNull(), { timeout: 10_000 })
  }, 15_000)

  test("registers the extension points the modules' pages need", () => {
    const globals = moduleRegistry.flatMap((e) => (e.global === undefined ? [] : [e.global.path]))
    expect(globals).toEqual(["tailnet", "derp", "cloudflare"])
    const actions = moduleRegistry.flatMap((e) => (e.containerActions ?? []).map((a) => a.id))
    expect(actions).toEqual(["firewall.allow", "firewall.deny", "npm.expose", "cloudflare.expose"])
    expect(moduleRegistry.flatMap((e) => (e.hostCreate === undefined ? [] : [e.hostCreate.id]))).toEqual(["tailnet.join"])
    expect(moduleRegistry.filter((e) => e.routes === undefined && e.global === undefined)).toEqual([])
  })
})

// m11Host runs the six M11 modules, console and jobs off as they ship.
function m11Host(scope: Scope): Host {
  const state = (id: string, enabled = true) => ({ id, version: 1, enabled, available: true, reason: null, checkedAt: null })
  return makeHost({
    scope,
    modules: [state("services"), state("users"), state("fail2ban"), state("console", false), state("jobs", false), state("tuning")],
  })
}

const READ_TABS = ["Services", "Users and SSH", "Intrusion prevention", "Jobs", "Tuning"]

describe("M11 tabs by role", () => {
  test.each<[string, Scope, Session]>([
    ["a viewer", "read", memberSession()],
    ["an operator", "operate", memberSession()],
    ["an admin", "admin", adminSession],
  ])("%s sees the tabs of their role", (who, scope, session) => {
    const tabs = hostTabs(moduleRegistry, m11Host(scope), session).map((e) => e.label)
    expect(tabs).toEqual(who === "an admin" ? ["Services", "Users and SSH", "Intrusion prevention", "Console", "Jobs", "Tuning"] : READ_TABS)
  })

  test("a module turned off has a tab only when it says what turning it on allows", () => {
    const host = m11Host("admin")
    host.modules = host.modules.map((m) => (m.id === "services" ? { ...m, enabled: false } : m))
    expect(hostTabs(moduleRegistry, host, adminSession).map((e) => e.id)).toEqual(["users", "fail2ban", "console", "jobs", "tuning"])
    expect(moduleView(moduleRegistry, host, "services")).toEqual({ kind: "not-enabled" })
    expect(moduleView(moduleRegistry, host, "jobs").kind).toBe("off")
  })

  test.each<[string, string, Scope, Session, boolean]>([
    ["console for an admin", "console", "admin", adminSession, true],
    ["jobs for an admin", "jobs", "admin", adminSession, true],
    ["jobs for a viewer", "jobs", "read", memberSession(), false],
    ["jobs for an operator", "jobs", "operate", memberSession(), false],
  ])("a module off on the host shows Off on this host: %s", async (_name, id, scope, session, enable) => {
    stubFetch({ "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [m11Host(scope)] } } } })
    renderWithProviders(
      <Routes>
        <Route path="/hosts/:hostId/:moduleId/*" element={<HostPage />} />
      </Routes>,
      { path: `/hosts/h1/${id}`, session },
    )
    const off = await screen.findByTestId("state-off")
    expect(within(off).getByText("Off on this host")).toBeTruthy()
    const entry = moduleRegistry.find((e) => e.id === id)
    expect(within(off).getByText(entry?.off?.allows ?? "")).toBeTruthy()
    expect(within(off).queryByRole("button", { name: "Enable" }) !== null).toBe(enable)
  })

  test("a member never reaches the console, even by its address", async () => {
    stubFetch({ "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [m11Host("operate")] } } } })
    renderWithProviders(
      <Routes>
        <Route path="/hosts/:hostId/:moduleId/*" element={<HostPage />} />
      </Routes>,
      { path: "/hosts/h1/console", session: memberSession() },
    )
    expect(await screen.findByText("Only admins can see this page.")).toBeTruthy()
    expect(screen.queryByTestId("state-off")).toBeNull()
    expect(screen.queryByRole("link", { name: "Console" })).toBeNull()
  })
})
