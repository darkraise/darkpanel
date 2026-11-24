import { screen, waitFor } from "@testing-library/react"
import { describe, expect, test } from "vitest"
import type { Change, Host, Scope, Session } from "@/api/types"
import { fwDocker, fwStatus } from "@/modules/firewall/fixtures"
import { StateControls } from "@/modules/firewall/state-controls"
import { npInstance } from "@/modules/npm/fixtures"
import { InstancePanel } from "@/modules/npm/instance-panel"
import { adminSession, memberSession, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { App } from "./app"

function hostWith(scope: Scope | ""): Host {
  return {
    id: "h1", name: "web-1", scope, isLocal: false, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0",
    protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, ingestion: null,
    modules: ["hostinfo", "firewall", "panel"].map((id) => ({ id, version: 1, enabled: true, available: true, reason: null, checkedAt: null })),
  }
}

const change: Change = {
  id: "c1", hostId: "h1", action: "docker.container.restart", version: 1, class: "compensable", state: "completed", actor: "user:u1",
  createdAt: "2026-11-11T08:00:00Z", updatedAt: "2026-11-11T08:00:00Z", revertDeadline: null, output: null, error: null,
  compensatedBy: "docker.container.restart", parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function renderApp(session: Session, path: string, host: Host = hostWith("read"), extra: Record<string, StubHandler> = {}) {
  stubFetch({
    "GET /api/v1/auth/session": { body: session },
    "GET /api/v1/controller/listeners": { body: { tailscaleBound: true, message: "Serving on Tailscale." } },
    "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [host] } } },
    "POST /api/v1/actions/core.update.rollouts/v1/read": { body: { output: { rollouts: [] } } },
    "POST /api/v1/hosts/h1/actions/core.update.status/v1/read": { body: { output: { transactions: [] } } },
    "GET /api/v1/changes/c1": { body: change },
    "GET /api/v1/changes/c1/effects": { body: { effects: [] } },
    ...extra,
  })
  renderWithProviders(<App />, { path, session: null })
}

const link = (name: string) => screen.queryAllByRole("link", { name }).length > 0
const ADMIN_ENTRIES = ["Users", "Host groups", "Sign-in", "Notifications"]
const NETWORK = ["Tailnet", "DERP", "Cloudflare"]

describe("member navigation", () => {
  test("an admin sees the admin settings, the Network pages and API tokens", async () => {
    renderApp(adminSession, "/", hostWith("admin"))
    await screen.findAllByRole("link", { name: "Users" })
    for (const name of [...ADMIN_ENTRIES, ...NETWORK, "API tokens", "Hosts", "Audit log", "Rollouts"]) expect(link(name), name).toBe(true)
  })

  test("a member sees Hosts, Audit, Rollouts and API tokens, no Network or admin settings", async () => {
    renderApp(memberSession(), "/")
    await screen.findAllByRole("link", { name: "API tokens" })
    for (const name of ["Hosts", "Audit log", "Rollouts", "API tokens"]) expect(link(name), name).toBe(true)
    for (const name of [...ADMIN_ENTRIES, ...NETWORK]) expect(link(name), name).toBe(false)
    expect(screen.queryByRole("button", { name: "Add host" })).toBeNull()
  })

  test.each(["/settings/users", "/settings/groups", "/settings/sign-in", "/settings/notifications", "/tailnet"])(
    "a member's deep link to %s says only admins can see it",
    async (path) => {
      renderApp(memberSession(), path)
      expect(await screen.findByText("Only admins can see this page.")).toBeTruthy()
    },
  )

  test.each([
    ["/settings/groups", "Host groups"],
    ["/settings/sign-in", "Sign-in"],
  ])("an admin's %s shows its page", async (path, title) => {
    renderApp(adminSession, path, hostWith("admin"))
    expect(await screen.findByRole("heading", { level: 1, name: title })).toBeTruthy()
    expect(screen.queryByText("This page is not built yet.")).toBeNull()
  })

  test("an admin reaches the admin settings routes", async () => {
    renderApp(adminSession, "/settings/notifications", hostWith("admin"))
    expect(await screen.findByText("This page is not built yet.")).toBeTruthy()
    expect(screen.queryByText("Only admins can see this page.")).toBeNull()
  })

  test("a member's rollouts page has no start button", async () => {
    renderApp(memberSession(), "/rollouts")
    await screen.findByText("No rollouts yet")
    expect(screen.queryByRole("button", { name: "Start rollout" })).toBeNull()
  })
})

describe("host pages render no buttons above the row's scope", () => {
  test("a read host's overview has no update or removal controls, and no admin module tab", async () => {
    renderApp(memberSession(), "/hosts/h1")
    await screen.findByRole("heading", { level: 1, name: "web-1" })
    expect(screen.queryByRole("button", { name: "Update agent" })).toBeNull()
    expect(screen.queryByRole("button", { name: "Change policy" })).toBeNull()
    expect(screen.queryByText("Enrollment and removal")).toBeNull()
    expect(screen.queryAllByRole("link", { name: "Firewall" }).length).toBeGreaterThan(0)
    expect(link("Panel exposure")).toBe(false)
  })

  test("an admin's overview keeps every control", async () => {
    renderApp(adminSession, "/hosts/h1", hostWith("admin"))
    expect(await screen.findByRole("button", { name: "Update agent" })).toBeTruthy()
    expect(screen.getByRole("button", { name: "Revoke host" })).toBeTruthy()
    expect(link("Panel exposure")).toBe(true)
  })

  test("a deep link to an admin module page says only admins can see it", async () => {
    renderApp(memberSession(), "/hosts/h1/panel", hostWith("operate"))
    expect(await screen.findByText("Only admins can see this page.")).toBeTruthy()
  })

  test("a change on a read host offers no undo; on an operate host it does", async () => {
    renderApp(memberSession(), "/changes/c1")
    await screen.findByRole("heading", { level: 1 })
    await waitFor(() => expect(screen.queryByText("Effects")).toBeTruthy())
    expect(screen.queryByRole("button", { name: "Undo" })).toBeNull()
  })

  test("a change on an operate host offers undo", async () => {
    renderApp(memberSession(), "/changes/c1", hostWith("operate"))
    expect(await screen.findByRole("button", { name: "Undo" })).toBeTruthy()
  })

  test("a read scope hides the firewall's controls, operate shows them", () => {
    stubFetch({})
    const read = renderWithProviders(<StateControls hostId="h1" status={fwStatus()} />, { hostScope: "read" })
    expect(screen.queryAllByRole("button")).toEqual([])
    read.unmount()
    renderWithProviders(<StateControls hostId="h1" status={fwStatus()} />, { hostScope: "operate" })
    for (const name of ["Disable", "Set default policy", "Set logging"]) expect(screen.getByRole("button", { name })).toBeTruthy()
  })

  test("operate offers the instance's operate actions, admin also its admin ones", async () => {
    const stubs = {
      "POST /api/v1/hosts/h1/actions/npm.instance.status/v1/read": { body: { output: npInstance() } },
      "GET /api/v1/hosts/h1/resources/firewall/docker": { body: fwDocker() },
      "GET /api/v1/hosts/h1/resources/firewall/status": { body: fwStatus() },
    }
    stubFetch(stubs)
    const operate = renderWithProviders(<InstancePanel hostId="h1" />, { hostScope: "operate" })
    expect(await screen.findByRole("button", { name: "Upgrade" })).toBeTruthy()
    expect(screen.queryByRole("button", { name: "Reveal admin password" })).toBeNull()
    operate.unmount()
    stubFetch(stubs)
    renderWithProviders(<InstancePanel hostId="h1" />, { hostScope: "admin" })
    expect(await screen.findByRole("button", { name: "Reveal admin password" })).toBeTruthy()
  })
})
