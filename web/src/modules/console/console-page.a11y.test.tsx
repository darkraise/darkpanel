import { act, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest"
import type { Host } from "@/api/types"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { ConsolePage } from "./console-page"
import type { TerminalSurface } from "./terminal"

const THEMES = ["light", "dark"] as const

class Sock {
  static OPEN = 1
  static last: Sock | null = null
  readyState = 0
  binaryType = "blob"
  onopen: (() => void) | null = null
  onmessage: (() => void) | null = null
  onclose: ((ev: { code: number; reason: string }) => void) | null = null
  constructor() {
    Sock.last = this
  }
  send() {}
  close() {
    this.readyState = 3
    this.onclose?.({ code: 1000, reason: "" })
  }
}

const surface: TerminalSurface = {
  cols: 80,
  rows: 24,
  write: () => {},
  onData: () => () => {},
  onResize: () => () => {},
  fit: () => {},
  focus: () => {},
  dispose: () => {},
}

function host(over: Partial<Host> = {}): Host {
  return {
    id: "h1", name: "web-1", scope: "admin", isLocal: false, state: "enrolled", online: true, lastSeenAt: null, agentVersion: "1.0.0",
    protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, ingestion: null,
    modules: [{ id: "console", version: 1, enabled: true, available: true, reason: null, checkedAt: null }],
    ...over,
  }
}

async function connect() {
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Authenticator code"), "123456")
  await user.click(screen.getByRole("button", { name: "Verify" }))
  await waitFor(() => expect(Sock.last).not.toBeNull())
  act(() => {
    if (Sock.last === null) return
    Sock.last.readyState = 1
    Sock.last.onopen?.()
  })
  await screen.findByText("Connected")
}

const STATES: [string, Host, () => Promise<void>][] = [
  ["asking a code", host(), async () => {
    await screen.findByLabelText("Authenticator code")
  }],
  ["connected", host(), connect],
  ["closed idle", host(), async () => {
    await connect()
    act(() => Sock.last?.onclose?.({ code: 1000, reason: "idle" }))
    await screen.findByRole("button", { name: "Reconnect" })
  }],
  ["host offline", host({ online: false }), async () => {
    await screen.findByText("The host is offline; reconnect once it is back")
  }],
  ["agent too old", host({ modules: [] }), async () => {
    await screen.findByText("Update the agent to use the terminal")
  }],
]

beforeEach(() => {
  Sock.last = null
})

afterEach(() => {
  document.documentElement.classList.remove("dark")
  vi.unstubAllGlobals()
})

describe("no axe violations in any state", () => {
  for (const theme of THEMES) {
    test.each(STATES)(`%s (${theme})`, async (_name, h, reach) => {
      document.documentElement.classList.toggle("dark", theme === "dark")
      stubFetch({
        "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [h] } } },
        "POST /api/v1/hosts/h1/channels/console/terminal/ticket": { body: { ticket: "T1" } },
      })
      vi.stubGlobal("WebSocket", Sock)
      const { container } = renderWithProviders(<ConsolePage hostId="h1" openSurface={() => surface} />)
      await reach()
      await expectNoAxeViolations(container)
      for (const dialog of document.querySelectorAll("[role=dialog]")) await expectNoAxeViolations(dialog)
    }, 20_000)
  }

  test("the key bar below 768 px (dark)", async () => {
    document.documentElement.classList.add("dark")
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("max-width"), media: q, addEventListener: () => {}, removeEventListener: () => {} }))
    stubFetch({
      "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [host()] } } },
      "POST /api/v1/hosts/h1/channels/console/terminal/ticket": { body: { ticket: "T1" } },
    })
    vi.stubGlobal("WebSocket", Sock)
    const { container } = renderWithProviders(<ConsolePage hostId="h1" openSurface={() => surface} />)
    await connect()
    await screen.findByRole("group", { name: "Terminal keys" })
    await expectNoAxeViolations(container)
  }, 20_000)
})
