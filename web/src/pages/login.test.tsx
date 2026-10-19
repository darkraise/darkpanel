import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { describe, expect, test } from "vitest"
import { request } from "@/api/client"
import type { Session } from "@/api/types"
import { RequireSession, SessionProvider } from "@/session/session"
import { safeNext } from "@/session/safe-next"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { LoginPage } from "./login"

const session: Session = {
  user: { id: "u1", username: "admin", role: "admin", state: "active", createdAt: "2026-10-19T08:00:00Z" },
  csrfToken: "csrf-1",
  expiresAt: "2026-10-19T20:00:00Z",
  token: null,
}

function problem(status: number, code: string, headers: Record<string, string> = {}): StubResponse {
  return { status, headers, body: { type: "about:blank", title: code, status, detail: `server detail for ${code}`, code } }
}

function app() {
  return (
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/" element={<RequireSession><p>Home content</p></RequireSession>} />
        <Route path="/audit" element={<RequireSession><p>Audit content</p></RequireSession>} />
      </Routes>
    </SessionProvider>
  )
}

async function logIn(code = "123456") {
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Username"), "admin")
  await user.type(screen.getByLabelText("Password"), "correct horse battery")
  await user.type(screen.getByLabelText("Authentication code"), code)
  await user.click(screen.getByRole("button", { name: "Log in" }))
}

const location = () => screen.getByTestId("location").textContent

describe("session guard and login", () => {
  test("redirects to the login page and returns to the page afterwards", async () => {
    const calls = stubFetch({
      "GET /api/v1/auth/session": problem(401, "auth.required"),
      "POST /api/v1/auth/login": { body: session },
    })
    renderWithProviders(app(), { path: "/audit?x=1" })
    await screen.findByLabelText("Username")
    expect(location()).toBe("/login?next=%2Faudit%3Fx%3D1")

    await logIn()
    expect((await screen.findByText("Audit content")).textContent).toBe("Audit content")
    expect(location()).toBe("/audit?x=1")
    const login = calls.find((c) => c.method === "POST" && c.path === "/api/v1/auth/login")
    expect(login?.body).toEqual({ username: "admin", password: "correct horse battery", code: "123456" })
  })

  test("sends the session's CSRF token on later unsafe requests", async () => {
    const calls = stubFetch({
      "GET /api/v1/auth/session": problem(401, "auth.required"),
      "POST /api/v1/auth/login": { body: session },
      "POST /api/v1/auth/logout": { status: 204 },
    })
    renderWithProviders(app(), { path: "/" })
    expect(location()).toBe("/")
    await logIn()
    await screen.findByText("Home content")
    await request("POST", "/api/v1/auth/logout")
    const logout = calls.find((c) => c.path === "/api/v1/auth/logout")
    expect(logout?.headers.get("X-CSRF-Token")).toBe("csrf-1")
    const loginCall = calls.find((c) => c.path === "/api/v1/auth/login")
    expect(loginCall?.headers.get("X-CSRF-Token")).toBeNull()
  })

  test("shows one generic error for wrong credentials", async () => {
    stubFetch({
      "GET /api/v1/auth/session": problem(401, "auth.required"),
      "POST /api/v1/auth/login": problem(401, "auth.invalid_credentials"),
    })
    renderWithProviders(app(), { path: "/login" })
    await logIn()
    const alert = await screen.findByRole("alert")
    expect(alert.textContent).toBe("Login failed. Check the username, password and code, then try again.")
    expect(screen.queryByText(/server detail/)).toBeNull()
    expect(location()).toBe("/login")
  })

  test("shows the wait time when rate limited", async () => {
    stubFetch({
      "GET /api/v1/auth/session": problem(401, "auth.required"),
      "POST /api/v1/auth/login": problem(429, "auth.rate_limited", { "Retry-After": "42" }),
    })
    renderWithProviders(app(), { path: "/login" })
    await logIn()
    expect((await screen.findByRole("alert")).textContent).toBe("Too many attempts. Try again in 42 seconds.")
  })

  test("an existing session skips the login page", async () => {
    stubFetch({ "GET /api/v1/auth/session": { body: session } })
    renderWithProviders(app(), { path: "/login?next=%2Faudit" })
    expect((await screen.findByText("Audit content")).textContent).toBe("Audit content")
  })

  test("a request answered auth.required ends the session", async () => {
    stubFetch({
      "GET /api/v1/auth/session": { body: session },
      "GET /api/v1/changes": problem(401, "auth.required"),
    })
    renderWithProviders(app(), { path: "/audit" })
    await screen.findByText("Audit content")
    await request("GET", "/api/v1/changes").catch(() => undefined)
    await waitFor(() => expect(location()).toBe("/login?next=%2Faudit"))
  })

  test("never follows a next value that leaves the app", async () => {
    stubFetch({
      "GET /api/v1/auth/session": problem(401, "auth.required"),
      "POST /api/v1/auth/login": { body: session },
    })
    renderWithProviders(app(), { path: "/login?next=%2F%2Fevil.example" })
    await logIn()
    await screen.findByText("Home content")
    expect(location()).toBe("/")
  })

  test.each([
    [null, "/"],
    ["/audit?x=1", "/audit?x=1"],
    ["/hosts/h1/hostinfo#top", "/hosts/h1/hostinfo#top"],
    ["//evil.example", "/"],
    ["/\\evil.example", "/"],
    ["/\t/evil.example", "/"],
    ["/\n/evil.example", "/"],
    ["/%2F%2Fevil.example", "/%2F%2Fevil.example"],
    ["https://evil.example", "/"],
    ["/login?next=%2F", "/"],
    ["/login/", "/"],
    ["/loginx", "/loginx"],
    ["audit", "/"],
  ])("safeNext(%j) is %s", (next, want) => {
    expect(safeNext(next)).toBe(want)
  })
})
