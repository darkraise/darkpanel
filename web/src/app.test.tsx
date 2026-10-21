import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test } from "vitest"
import type { Host, Session } from "@/api/types"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { App } from "./app"

const session: Session = {
  user: { id: "u1", username: "admin", role: "admin", state: "active", createdAt: "2026-10-19T08:00:00Z" },
  csrfToken: "csrf-1",
  expiresAt: "2026-10-19T20:00:00Z",
  token: null,
}

const host: Host = {
  id: "h1", name: "web-1", isLocal: true, online: true,
  modules: [{ id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null }],
  ingestion: null,
}

function stubServer(sessionResponse: StubResponse) {
  return stubFetch({
    "GET /api/v1/auth/session": sessionResponse,
    "GET /api/v1/controller/listeners": { body: { tailscaleBound: false, message: "No Tailscale address found; serving loopback only." } },
    "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [host] } } },
    "POST /api/v1/actions/core.audit.list/v1/read": { body: { output: { entries: [], nextBefore: null } } },
    "POST /api/v1/actions/core.token.list/v1/read": { body: { output: { tokens: [] } } },
    "GET /api/v1/hosts/h1/resources/hostinfo/summary": {
      body: {
        hostname: "web-1", os: { id: "ubuntu", versionId: "22.04", prettyName: "Ubuntu 22.04.5 LTS" }, kernel: "5.15.0",
        arch: "amd64", uptimeSeconds: 60, bootTime: "2026-10-19T08:00:00Z", cpu: { model: "QEMU", logicalCount: 2 },
        memory: { totalBytes: 2147483648, availableBytes: 1073741824 }, swap: { totalBytes: 0, freeBytes: 0 },
        load: { one: 0, five: 0, fifteen: 0 }, rootFs: { totalBytes: 10737418240, availableBytes: 5368709120 }, darkpanelVersion: "0.1.0",
      },
    },
  })
}

const loggedOut: StubResponse = {
  status: 401,
  body: { type: "about:blank", title: "Authentication required", status: 401, detail: "log in first", code: "auth.required" },
}

const heading = (name: string) => screen.findByRole("heading", { level: 1, name })

describe("app routes", () => {
  test("without a session every page leads to the login page", async () => {
    stubServer(loggedOut)
    renderWithProviders(<App />, { path: "/settings/tokens" })
    expect((await screen.findByLabelText("Username")).getAttribute("name")).toBe("username")
    expect(screen.getByTestId("location").textContent).toBe("/login?next=%2Fsettings%2Ftokens")
  })

  test("the hosts overview is the home page and links to the host", async () => {
    stubServer({ body: session })
    renderWithProviders(<App />, { path: "/" })
    expect((await heading("Hosts")).textContent).toBe("Hosts")
    await userEvent.setup().click(await screen.findByRole("link", { name: "web-1" }))
    expect((await screen.findByText("Ubuntu 22.04.5 LTS")).textContent).toBe("Ubuntu 22.04.5 LTS")
    expect(screen.getByTestId("location").textContent).toBe("/hosts/h1/hostinfo")
  })

  test.each([
    ["/audit", "Audit log"],
    ["/settings/tokens", "API tokens"],
  ])("%s shows %s", async (path, title) => {
    stubServer({ body: session })
    renderWithProviders(<App />, { path })
    expect((await heading(title)).textContent).toBe(title)
  })

  test("an unknown address shows page not found inside the shell", async () => {
    stubServer({ body: session })
    renderWithProviders(<App />, { path: "/nope" })
    expect((await screen.findByText("Page not found")).textContent).toBe("Page not found")
    expect(screen.getAllByRole("link", { name: "Hosts" }).length).toBeGreaterThan(0)
  })
})
