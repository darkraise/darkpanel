import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { describe, expect, test } from "vitest"
import type { Session } from "@/api/types"
import { RequireSession, SessionProvider } from "@/session/session"
import { renderWithProviders, stubFetch, type StubHandler, type StubResponse } from "@/test/utils"
import { LoginPage } from "./login"

const session: Session = {
  user: { id: "u1", username: "mia", role: "member", allHosts: "", state: "active", createdAt: "2026-10-19T08:00:00Z" },
  csrfToken: "csrf-1",
  expiresAt: "2026-10-19T20:00:00Z",
  token: null,
}

function problem(status: number, code: string): StubResponse {
  return { status, body: { type: "about:blank", title: code, status, detail: `server detail for ${code}`, code } }
}

const signedOut = problem(401, "auth.required")
const methods = (oidc: string | null, tailscale = false): StubResponse => ({ body: { oidc: oidc === null ? null : { displayName: oidc }, tailscale } })
const matchOf = (fields: { remembered: boolean; codeReady: boolean; username?: string; nodeName?: string }): StubResponse => ({
  body: { username: "mia", loginName: "mia@github", nodeName: "laptop", ...fields },
})
const match = matchOf({ remembered: true, codeReady: true })
const NOTE = "Continue as needs your authenticator code once. Finish account setup or sign in below."

function app() {
  return (
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/" element={<RequireSession><p>Hosts content</p></RequireSession>} />
      </Routes>
    </SessionProvider>
  )
}

function render(path: string, routes: Record<string, StubHandler>) {
  const calls = stubFetch({ "GET /api/v1/auth/session": signedOut, ...routes })
  renderWithProviders(app(), { path })
  return calls
}

const location = () => screen.getByTestId("location").textContent

describe("OIDC button", () => {
  test("shows below the form only when the methods answer names a provider", async () => {
    render("/login", { "GET /api/v1/auth/methods": methods("Acme SSO") })
    const link = await screen.findByRole("link", { name: "Sign in with Acme SSO" })
    expect(link.getAttribute("href")).toBe("/api/v1/auth/oidc/start")
    const login = screen.getByRole("button", { name: "Log in" })
    expect(login.compareDocumentPosition(link) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  test.each([
    ["OIDC is off here", methods(null)],
    ["the methods read fails", problem(500, "internal")],
  ])("is absent when %s", async (_, answer) => {
    const calls = render("/login", { "GET /api/v1/auth/methods": answer })
    await screen.findByLabelText("Username")
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/auth/methods")).toBe(true))
    expect(screen.queryByRole("link", { name: /^Sign in with/ })).toBeNull()
    expect(calls.some((c) => c.path === "/api/v1/auth/tailscale")).toBe(false)
    expect(screen.getByRole("button", { name: "Log in" })).toBeTruthy()
  })
})

describe("sign-in outcomes", () => {
  test("UX-3 no linked user shows the copy with focus on it, and leaves the address", async () => {
    render("/?signin=no_linked_user", { "GET /api/v1/auth/methods": methods("Acme SSO") })
    const alert = await screen.findByText("No Darkpanel user is linked to this account. Ask an admin to add you.")
    await waitFor(() => expect(document.activeElement?.getAttribute("role")).toBe("alert"))
    expect(document.activeElement?.contains(alert)).toBe(true)
    await waitFor(() => expect(location()).toBe("/login"))
    expect(screen.getByLabelText("Username")).toBeTruthy()
  })

  test.each([
    ["method_off", "This sign-in method is off. Use your username, password and code."],
    ["refused", "The sign-in was refused. Try again, or use your username, password and code."],
  ])("%s shows its message", async (outcome, text) => {
    render(`/?signin=${outcome}`, {})
    expect(await screen.findByText(text)).toBeTruthy()
  })

  test("UX-3 totp shows the code step first, and its code signs in onto Hosts", async () => {
    let wrong = true
    const calls = render("/?signin=totp", {
      "POST /api/v1/auth/totp": () => (wrong ? problem(401, "auth.invalid_credentials") : { body: session }),
    })
    expect(await screen.findByText("Enter the code from your authenticator app to finish signing in.")).toBeTruthy()
    expect(screen.queryByLabelText("Username")).toBeNull()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText("Authentication code"), "111111")
    await user.click(screen.getByRole("button", { name: "Log in" }))
    expect(await screen.findByText("That code did not work. Try the current one, or sign in again.")).toBeTruthy()
    wrong = false
    await user.clear(screen.getByLabelText("Authentication code"))
    await user.type(screen.getByLabelText("Authentication code"), "123456")
    await user.click(screen.getByRole("button", { name: "Log in" }))
    expect(await screen.findByText("Hosts content")).toBeTruthy()
    expect(location()).toBe("/")
    expect(calls.filter((c) => c.path === "/api/v1/auth/totp").map((c) => c.body)).toEqual([{ code: "111111" }, { code: "123456" }])
  })

  test("the code step offers the other ways back", async () => {
    render("/?signin=totp", {})
    await userEvent.setup().click(await screen.findByRole("button", { name: "Use another way to sign in" }))
    expect(await screen.findByLabelText("Username")).toBeTruthy()
  })

  test("ok with a session lands on Hosts without the outcome in the address", async () => {
    stubFetch({ "GET /api/v1/auth/session": { body: session } })
    renderWithProviders(app(), { path: "/?signin=ok" })
    expect(await screen.findByText("Hosts content")).toBeTruthy()
    expect(location()).toBe("/")
  })
})

describe("continue as", () => {
  test("shows above the form on a match and posts JSON to sign in", async () => {
    const calls = render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": match,
      "POST /api/v1/auth/tailscale": { body: session },
    })
    const button = await screen.findByRole("button", { name: "Continue as mia (mia@github, from laptop)" })
    expect(button.compareDocumentPosition(screen.getByLabelText("Username")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    await userEvent.setup().click(button)
    expect(await screen.findByText("Hosts content")).toBeTruthy()
    const post = calls.find((c) => c.method === "POST" && c.path === "/api/v1/auth/tailscale")
    expect(post?.body).toEqual({})
    expect(post?.headers.get("Content-Type")).toBe("application/json")
  })

  test("UX-2 with requireTotp it asks for the code as the matched user, with no remember line", async () => {
    render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": match,
      "POST /api/v1/auth/tailscale": { body: { totp: true, remember: false } },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: /^Continue as mia/ }))
    expect(await screen.findByText("Enter the code from your authenticator app to finish signing in as mia.")).toBeTruthy()
    expect(screen.queryByText(/^Enter your code once to remember/)).toBeNull()
    await user.click(screen.getByRole("button", { name: "Use another way to sign in" }))
    const again = await screen.findByRole("button", { name: /^Continue as mia/ })
    expect(again.hasAttribute("disabled") || again.getAttribute("aria-busy") === "true", "Continue as is ready again").toBe(false)
  })

  test("a refused continue says so and keeps the form", async () => {
    render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": match,
      "POST /api/v1/auth/tailscale": problem(401, "auth.invalid_credentials"),
    })
    await userEvent.setup().click(await screen.findByRole("button", { name: /^Continue as mia/ }))
    expect(await screen.findByText("The sign-in was refused. Try again, or use your username, password and code.")).toBeTruthy()
    expect(screen.getByLabelText("Username")).toBeTruthy()
  })

  test("nothing shows without a match", async () => {
    const calls = render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": problem(401, "auth.invalid_credentials"),
    })
    await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/auth/tailscale")).toBe(true))
    await screen.findByLabelText("Username")
    expect(screen.queryByRole("button", { name: /^Continue as/ })).toBeNull()
  })

  test.each([
    ["a named node", "laptop.tail1234.ts.net", "Enter your code once to remember laptop."],
    ["an empty node name", "", "Enter your code once to remember this device."],
  ])("UX-1 a code-ready user on a node not remembered gets the code step with its remember line, for %s", async (_, nodeName, line) => {
    const calls = render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": matchOf({ remembered: false, codeReady: true, nodeName }),
      "POST /api/v1/auth/tailscale": { body: { totp: true, remember: true } },
      "POST /api/v1/auth/totp": { body: session },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: `Continue as mia (mia@github, from ${nodeName})` }))
    const instruction = await screen.findByText("Enter the code from your authenticator app to finish signing in as mia.")
    const remember = screen.getByText(line)
    expect(instruction.compareDocumentPosition(remember) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    await user.type(screen.getByLabelText("Authentication code"), "123456")
    await user.click(screen.getByRole("button", { name: "Log in" }))
    expect(await screen.findByText("Hosts content")).toBeTruthy()
    expect(calls.filter((c) => c.path === "/api/v1/auth/totp").map((c) => c.body)).toEqual([{ code: "123456" }])
  })

  test("UX-1 Back from the remember step returns to the login page", async () => {
    const calls = render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": matchOf({ remembered: false, codeReady: true }),
      "POST /api/v1/auth/tailscale": { body: { totp: true, remember: true } },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: /^Continue as mia/ }))
    await screen.findByText("Enter your code once to remember laptop.")
    await user.click(screen.getByRole("button", { name: "Use another way to sign in" }))
    expect(await screen.findByLabelText("Username")).toBeTruthy()
    expect(screen.getByRole("button", { name: /^Continue as mia/ })).toBeTruthy()
    expect(calls.some((c) => c.path === "/api/v1/auth/totp")).toBe(false)
  })

  test.each([
    ["A19 neither remembered nor code-ready", matchOf({ remembered: false, codeReady: false })],
    ["A3 a pending user", matchOf({ remembered: false, codeReady: false, username: "zoe" })],
  ])("UX-3 %s: no button, the note, and the password form still signs in", async (_, answer) => {
    const calls = render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": answer,
      "POST /api/v1/auth/login": { body: session },
    })
    expect(await screen.findByText(NOTE)).toBeTruthy()
    expect(screen.queryByRole("button", { name: /^Continue as/ })).toBeNull()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText("Username"), "mia")
    await user.type(screen.getByLabelText("Password"), "correct horse")
    await user.type(screen.getByLabelText("Authentication code"), "123456")
    await user.click(screen.getByRole("button", { name: "Log in" }))
    expect(await screen.findByText("Hosts content")).toBeTruthy()
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/v1/auth/tailscale")).toBe(false)
  })

  test("UX-6 while the match is loading there is no button and no note, and the form is usable", async () => {
    render("/login", {
      "GET /api/v1/auth/methods": methods(null, true),
      "GET /api/v1/auth/tailscale": () => new Promise<StubResponse>(() => undefined),
    })
    const username = await screen.findByLabelText("Username")
    await userEvent.setup().type(username, "mia")
    expect((username as HTMLInputElement).value).toBe("mia")
    expect(screen.queryByRole("button", { name: /^Continue as/ })).toBeNull()
    expect(screen.queryByText(NOTE)).toBeNull()
  })
})
