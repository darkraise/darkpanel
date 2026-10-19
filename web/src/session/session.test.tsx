import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { request } from "@/api/client"
import type { Session } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { SessionProvider, useSession } from "./session"

function sessionWith(csrfToken: string): Session {
  return {
    user: { id: "u1", username: "admin", role: "admin", state: "active", createdAt: "2026-10-19T08:00:00Z" },
    csrfToken,
    expiresAt: "2026-10-19T20:00:00Z",
    token: null,
  }
}

function Sender() {
  const { status } = useSession()
  if (status === "loading") return null
  return <button onClick={() => void request("POST", "/api/v1/ping").catch(() => undefined)}>Send</button>
}

test("a rejected CSRF token is replaced by one re-probe of the session", async () => {
  let probes = 0
  const calls = stubFetch({
    "GET /api/v1/auth/session": () => ({ body: sessionWith(++probes === 1 ? "csrf-old" : "csrf-new") }),
    "POST /api/v1/ping": (call) =>
      call.headers.get("X-CSRF-Token") === "csrf-new"
        ? { status: 204 }
        : { status: 403, body: { type: "about:blank", title: "CSRF", status: 403, detail: "bad csrf token", code: "auth.csrf" } },
  })
  renderWithProviders(
    <SessionProvider>
      <Sender />
    </SessionProvider>,
  )
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Send" }))
  await waitFor(() => expect(probes).toBe(2))

  await user.click(screen.getByRole("button", { name: "Send" }))
  await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(2))
  const pings = calls.filter((c) => c.method === "POST")
  expect(pings.map((c) => c.headers.get("X-CSRF-Token"))).toEqual(["csrf-old", "csrf-new"])
  expect(probes).toBe(2)
})

test("a failing re-probe ends the session", async () => {
  let probes = 0
  stubFetch({
    "GET /api/v1/auth/session": () =>
      ++probes === 1
        ? { body: sessionWith("csrf-old") }
        : { status: 401, body: { type: "about:blank", title: "Auth", status: 401, detail: "log in", code: "auth.required" } },
    "POST /api/v1/ping": { status: 403, body: { type: "about:blank", title: "CSRF", status: 403, detail: "bad csrf token", code: "auth.csrf" } },
  })
  function Probe() {
    const { session } = useSession()
    return <p>{session === null ? "signed out" : "signed in"}</p>
  }
  renderWithProviders(
    <SessionProvider>
      <Sender />
      <Probe />
    </SessionProvider>,
  )
  await userEvent.setup().click(await screen.findByRole("button", { name: "Send" }))
  await screen.findByText("signed out")
  expect(probes).toBe(2)
})
