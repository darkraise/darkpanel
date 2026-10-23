import { act, cleanup, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import type { ListenerState, Session } from "@/api/types"
import { applyBrand } from "@/brand"
import { RequireSession, SessionProvider } from "@/session/session"
import { EvtSource } from "@/test/evt-source"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { Shell } from "./shell"

const session: Session = {
  user: { id: "u1", username: "admin", role: "admin", state: "active", createdAt: "2026-10-19T08:00:00Z" },
  csrfToken: "csrf-1",
  expiresAt: "2026-10-19T20:00:00Z",
  token: null,
}

const bound: ListenerState = { tailscaleBound: true, message: "Serving on Tailscale: https://100.64.0.1:8443." }
const loopbackOnly: ListenerState = { tailscaleBound: false, message: "No Tailscale address found; serving loopback only." }

function problem(status: number, code: string, detail: string): StubResponse {
  return { status, body: { type: "about:blank", title: code, status, detail, code } }
}

function renderShell(
  listeners: StubResponse,
  path = "/",
  logout: StubResponse = { status: 204 },
) {
  const calls = stubFetch({
    "GET /api/v1/auth/session": { body: session },
    "GET /api/v1/controller/listeners": listeners,
    "POST /api/v1/auth/logout": logout,
  })
  renderWithProviders(
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<p>Login page</p>} />
        <Route element={<RequireSession><Shell /></RequireSession>}>
          <Route index element={<p>Hosts content</p>} />
          <Route path="audit" element={<p>Audit content</p>} />
        </Route>
      </Routes>
    </SessionProvider>,
    { path },
  )
  return calls
}

// The user menu's trigger shows the user's initials, so "admin" is "A".
async function openUserMenu(user: ReturnType<typeof userEvent.setup>) {
  const trigger = screen.getAllByRole("button", { name: "A" })[0]
  if (trigger === undefined) throw new Error("no user menu")
  await user.click(trigger)
}

async function logOut(user: ReturnType<typeof userEvent.setup>) {
  await openUserMenu(user)
  await user.click(await screen.findByRole("menuitem", { name: "Log out" }))
}

test("the sidebar brand reads Darkpanel", async () => {
  renderShell({ body: bound })
  await screen.findByText("Hosts content")
  expect(screen.queryByText("Darkpanel")).toBeNull()
  act(() => applyBrand())
  expect((await screen.findAllByText("Darkpanel")).length).toBeGreaterThan(0)
})

test("the listener state is not polled, only refreshed on window focus", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  try {
    const calls = renderShell({ body: bound })
    await screen.findByText("Hosts content")
    await vi.advanceTimersByTimeAsync(61_000)
    expect(calls.filter((c) => c.method === "GET" && c.path === "/api/v1/controller/listeners")).toHaveLength(1)
  } finally {
    vi.useRealTimers()
  }
})

test("shows the navigation with the current page marked", async () => {
  renderShell({ body: bound }, "/audit")
  await screen.findByText("Audit content")
  const audit = screen.getAllByRole("link", { name: "Audit log" })[0]
  expect(audit?.getAttribute("href")).toBe("/audit")
  expect(audit?.getAttribute("aria-current")).toBe("page")
  expect(screen.getAllByRole("link", { name: "Hosts" })[0]?.getAttribute("href")).toBe("/")
  expect(screen.getAllByRole("link", { name: "API tokens" })[0]?.getAttribute("href")).toBe("/settings/tokens")
})

test.each([
  ["bound", bound, loopbackOnly],
  ["loopback only", loopbackOnly, bound],
])("the footer shows the controller's listener message when %s", async (_, shown, other) => {
  const calls = renderShell({ body: shown })
  expect((await screen.findAllByText(shown.message)).length).toBeGreaterThan(0)
  expect(screen.queryByText(other.message)).toBeNull()
  expect(calls.some((c) => c.method === "GET" && c.path === "/api/v1/controller/listeners")).toBe(true)
})

test("the footer reports a failed listener request", async () => {
  renderShell(problem(403, "auth.forbidden", "this request needs the read scope"))
  expect((await screen.findAllByText("Listener state unavailable: this request needs the read scope")).length).toBeGreaterThan(0)
})

test("the user menu names the user and their role", async () => {
  renderShell({ body: bound })
  await screen.findByText("Hosts content")
  await openUserMenu(userEvent.setup())
  expect((await screen.findByText("Role: admin")).textContent).toBe("Role: admin")
})

test("logging out ends the session and returns to the login page", async () => {
  const calls = renderShell({ body: bound })
  await screen.findByText("Hosts content")
  await logOut(userEvent.setup())
  await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/login"))
  const logout = calls.find((c) => c.path === "/api/v1/auth/logout")
  expect(logout?.method).toBe("POST")
  expect(logout?.headers.get("X-CSRF-Token")).toBe("csrf-1")
})

test.each([
  ["confirmed", { status: 204 }],
  ["answered with auth.required", problem(401, "auth.required", "log in")],
])("a logout %s goes from the page straight to the login page", async (_, answer) => {
  renderShell({ body: bound }, "/audit", answer)
  await screen.findByText("Audit content")
  await logOut(userEvent.setup())
  await screen.findByText("Login page")
  expect(screen.getByTestId("locations").textContent).toBe("/audit /login")
})

test("a failed logout keeps the session and says why", async () => {
  renderShell({ body: bound }, "/", problem(500, "internal", "Internal error"))
  await screen.findByText("Hosts content")
  await logOut(userEvent.setup())
  expect((await screen.findByText("Logout failed: Internal error")).textContent).toBe("Logout failed: Internal error")
  expect(screen.getByText("Hosts content").textContent).toBe("Hosts content")
  expect(screen.getByTestId("location").textContent).toBe("/")
})

test("the signed-in shell keeps one event stream open while it is mounted", async () => {
  renderShell({ body: bound })
  await screen.findByText("Hosts content")
  expect(EvtSource.instances.map((s) => s.url)).toEqual(["/api/v1/events"])
  expect(EvtSource.instances[0]?.closed).toBe(false)
  cleanup()
  expect(EvtSource.instances[0]?.closed).toBe(true)
})
