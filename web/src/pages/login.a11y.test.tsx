import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { SessionProvider } from "@/session/session"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { LoginPage } from "./login"

function problem(status: number, code: string, headers: Record<string, string> = {}): StubResponse {
  return { status, headers, body: { type: "about:blank", title: code, status, detail: `server detail for ${code}`, code } }
}

function renderLogin(login: StubResponse = problem(401, "auth.invalid_credentials")) {
  stubFetch({ "GET /api/v1/auth/session": problem(401, "auth.required"), "POST /api/v1/auth/login": login })
  return renderWithProviders(
    <SessionProvider>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
      </Routes>
    </SessionProvider>,
    { path: "/login" },
  )
}

test("the login form passes axe", async () => {
  const { container } = renderLogin()
  await screen.findByLabelText("Username")
  await expectNoAxeViolations(container)
})

test("the login form passes axe while the session is being read", async () => {
  stubFetch({ "GET /api/v1/auth/session": () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(
    <SessionProvider>
      <LoginPage />
    </SessionProvider>,
  )
  await screen.findByLabelText("Loading")
  await expectNoAxeViolations(container)
})

test("the login form passes axe with its error shown", async () => {
  const { container } = renderLogin()
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Username"), "admin")
  await user.type(screen.getByLabelText("Password"), "wrong horse")
  await user.type(screen.getByLabelText("Authentication code"), "123456")
  await user.click(screen.getByRole("button", { name: "Log in" }))
  expect((await screen.findByRole("alert")).textContent).toBe("Login failed. Check the username, password and code, then try again.")
  await expectNoAxeViolations(container)
})

test("the keyboard reaches the fields in reading order, starting on the username", async () => {
  renderLogin()
  const user = userEvent.setup()
  await screen.findByLabelText("Username")
  expect(focusedName()).toBe("Username")
  expect(await tabSequence(user, 3)).toEqual(["Password", "Authentication code", "Log in"])
  expect(await tabSequence(user, 2, { backwards: true })).toEqual(["Authentication code", "Password"])
})

test("the keyboard alone submits the form", async () => {
  renderLogin()
  const user = userEvent.setup()
  await screen.findByLabelText("Username")
  await user.keyboard("admin")
  await user.tab()
  await user.keyboard("correct horse battery")
  await user.tab()
  await user.keyboard("123456{Enter}")
  expect((await screen.findByRole("alert")).textContent).toContain("Login failed")
})

test("the page title is Sign in", async () => {
  renderLogin()
  await screen.findByLabelText("Username")
  expect(document.title).toBe("Sign in · Darkpanel")
})
