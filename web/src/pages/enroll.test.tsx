import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useLocation } from "react-router"
import { expect, test } from "vitest"
import type { Session } from "@/api/types"
import { App } from "@/app"
import { adminSession, renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"

const URI = "otpauth://totp/Darkpanel:noor?secret=JBSWY3DPEHPK3PXP&issuer=Darkpanel"
const noorSession: Session = { ...adminSession, user: { ...adminSession.user, id: "u-noor", username: "noor", role: "member", allHosts: "" } }
const problem = (status: number, code: string, detail: string, errors?: { path: string; message: string }[]) => ({
  status,
  body: { type: "about:blank", title: code, status, detail, code, errors },
})

function HashProbe() {
  return <output data-testid="hash">{useLocation().hash}</output>
}

function renderEnroll(path: string, routes: Record<string, StubHandler>): FetchCall[] {
  let signedIn = false
  const calls = stubFetch({
    "GET /api/v1/auth/session": () => (signedIn ? { body: noorSession } : problem(401, "auth.required", "log in first")),
    "GET /api/v1/controller/listeners": { body: { tailscaleBound: true, message: "Serving on Tailscale." } },
    "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [] } } },
    ...routes,
    "POST /api/v1/enroll/finish": (call) => {
      const handler = routes["POST /api/v1/enroll/finish"]
      if (handler === undefined) return { status: 404 }
      signedIn = true
      return typeof handler === "function" ? handler(call) : handler
    },
  })
  renderWithProviders(
    <>
      <App />
      <HashProbe />
    </>,
    { path, session: null },
  )
  return calls
}

const begin = { body: { username: "noor", secret: "JBSWY3DPEHPK3PXP", uri: URI, expiresAt: "2026-11-26T09:40:00Z" } }

test("UX-2 enrollment: a valid link shows the password, QR and code fields; success lands on Hosts", async () => {
  const calls = renderEnroll("/enroll#tok-123", {
    "POST /api/v1/enroll/begin": begin,
    "POST /api/v1/enroll/finish": { body: noorSession },
  })
  expect(await screen.findByText("You are joining as noor. Choose a password, then add the code.")).toBeTruthy()
  expect(screen.getByRole("img", { name: "QR code for your authenticator app" })).toBeTruthy()
  expect(screen.getByText("JBSW Y3DP EHPK 3PXP")).toBeTruthy()
  await waitFor(() => expect(screen.getByTestId("hash").textContent).toBe(""))
  const user = userEvent.setup()
  await user.type(screen.getByLabelText("Password"), "a long password")
  await user.type(screen.getByLabelText("Confirm password"), "a long password")
  await user.type(screen.getByLabelText("Authentication code"), "123456")
  await user.click(screen.getByRole("button", { name: "Finish setup" }))
  await screen.findByRole("heading", { level: 1, name: "Hosts" })
  expect(calls.find((c) => c.path === "/api/v1/enroll/begin")?.body).toEqual({ token: "tok-123" })
  expect(calls.find((c) => c.path === "/api/v1/enroll/finish")?.body).toEqual({ token: "tok-123", password: "a long password", code: "123456" })
  expect(calls.some((c) => c.path.includes("tok-123"))).toBe(false)
})

test("the form checks the password and code before sending", async () => {
  const calls = renderEnroll("/enroll#tok-123", { "POST /api/v1/enroll/begin": begin })
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Password"), "short")
  await user.type(screen.getByLabelText("Confirm password"), "other")
  await user.click(screen.getByRole("button", { name: "Finish setup" }))
  expect(screen.getByText("Use at least 12 characters.")).toBeTruthy()
  expect(screen.getByText("The passwords do not match.")).toBeTruthy()
  expect(screen.getByText("Enter the 6-digit code from your authenticator app.")).toBeTruthy()
  expect(calls.some((c) => c.path === "/api/v1/enroll/finish")).toBe(false)
})

test("a wrong code says so and keeps the form", async () => {
  renderEnroll("/enroll#tok-123", {
    "POST /api/v1/enroll/begin": begin,
    "POST /api/v1/enroll/finish": problem(400, "request.invalid_input", "invalid input", [{ path: "/code", message: "does not match the authenticator" }]),
  })
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Password"), "a long password")
  await user.type(screen.getByLabelText("Confirm password"), "a long password")
  await user.type(screen.getByLabelText("Authentication code"), "000000")
  await user.click(screen.getByRole("button", { name: "Finish setup" }))
  expect(await screen.findByText("That code does not match. Try the current one.")).toBeTruthy()
  expect(screen.getByLabelText("Password")).toBeTruthy()
})

test.each([
  ["an expired or used link", "/enroll#tok-old"],
  ["a link without a token", "/enroll"],
])("UX-2: %s shows the expired message and no form", async (_, path) => {
  renderEnroll(path, { "POST /api/v1/enroll/begin": problem(410, "auth.enrollment_expired", "Enrollment link expired") })
  expect(await screen.findByText("This link has expired. Ask an admin for a new one.")).toBeTruthy()
  expect(screen.queryByLabelText("Password")).toBeNull()
  expect(screen.queryByRole("button", { name: "Finish setup" })).toBeNull()
})
