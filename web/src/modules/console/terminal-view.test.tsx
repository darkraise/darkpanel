import { act, fireEvent, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import type { TerminalSurface } from "./terminal"
import { STEP_UP_REASON, TerminalView } from "./terminal-view"

const TICKET = "POST /api/v1/hosts/h1/channels/console/terminal/ticket"

// FakeSocket stands in for WebSocket: a test opens it, sends it server
// messages and closes it with a reason.
class FakeSocket {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3
  static instances: FakeSocket[] = []
  readonly url: string
  readyState = 0
  binaryType = "blob"
  sent: (string | Uint8Array)[] = []
  onopen: (() => void) | null = null
  onmessage: ((ev: { data: ArrayBuffer | string }) => void) | null = null
  onclose: ((ev: { code: number; reason: string }) => void) | null = null

  constructor(url: string) {
    this.url = url
    FakeSocket.instances.push(this)
  }

  send(data: string | Uint8Array) {
    this.sent.push(typeof data === "string" ? data : new Uint8Array(data))
  }

  close(code = 1000, reason = "") {
    this.end(code, reason)
  }

  open() {
    this.readyState = 1
    this.onopen?.()
  }

  end(code: number, reason: string) {
    if (this.readyState === 3) return
    this.readyState = 3
    this.onclose?.({ code, reason })
  }

  text(): string[] {
    return this.sent.map((s) => (typeof s === "string" ? s : new TextDecoder().decode(s)))
  }
}

interface Fake extends TerminalSurface {
  written: string[]
  type: (data: string) => void
  resize: (cols: number, rows: number) => void
}

let surface: Fake

function fakeSurface(): Fake {
  let data: (d: string) => void = () => {}
  let resize: (c: number, r: number) => void = () => {}
  const s: Fake = {
    cols: 100,
    rows: 30,
    written: [],
    write: (d) => s.written.push(typeof d === "string" ? d : new TextDecoder().decode(d)),
    onData: (cb) => {
      data = cb
      return () => {}
    },
    onResize: (cb) => {
      resize = cb
      return () => {}
    },
    fit: vi.fn(),
    focus: vi.fn(),
    dispose: vi.fn(),
    type: (d) => data(d),
    resize: (c, r) => resize(c, r),
  }
  return s
}

function socket(): FakeSocket {
  const s = FakeSocket.instances[FakeSocket.instances.length - 1]
  if (s === undefined) throw new Error("no socket opened")
  return s
}

function render(over: { online?: boolean; reported?: boolean } = {}) {
  const calls = stubFetch({ [TICKET]: { body: { ticket: "T1" } } })
  vi.stubGlobal("WebSocket", FakeSocket)
  renderWithProviders(<TerminalView hostId="h1" hostName="web-1" online={over.online ?? true} reported={over.reported ?? true} openSurface={() => surface} />)
  return calls
}

async function connect() {
  const user = userEvent.setup()
  await user.type(await screen.findByLabelText("Authenticator code"), "123456")
  await user.click(screen.getByRole("button", { name: "Verify" }))
  await waitFor(() => expect(FakeSocket.instances.length).toBeGreaterThan(0))
  act(() => socket().open())
  await screen.findByText("Connected")
  return user
}

beforeEach(() => {
  FakeSocket.instances = []
  surface = fakeSurface()
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("states and reasons", () => {
  test("opening asks a code, posts the ticket request and never puts the code in the URL", async () => {
    const calls = render()
    expect(await screen.findByText(STEP_UP_REASON)).toBeTruthy()
    expect(screen.getByText("Waiting for your code")).toBeTruthy()
    const user = userEvent.setup()
    await user.type(screen.getByLabelText("Authenticator code"), "123456")
    await user.click(screen.getByRole("button", { name: "Verify" }))
    await waitFor(() => expect(FakeSocket.instances).toHaveLength(1))
    expect(calls.find((c) => c.path.endsWith("/terminal/ticket"))?.body).toEqual({ code: "123456" })
    const url = new URL(socket().url)
    expect(url.pathname).toBe("/api/v1/hosts/h1/channels/console/terminal")
    expect(Object.fromEntries(url.searchParams)).toEqual({ ticket: "T1", cols: "100", rows: "30" })
    expect(socket().url).not.toContain("123456")
    expect(screen.getByText("Connecting…")).toBeTruthy()
    act(() => socket().open())
    expect(await screen.findByText("Connected")).toBeTruthy()
    expect(surface.focus).toHaveBeenCalled()
  })

  test("typed data goes out as binary, output is written and resize sends a resize message", async () => {
    render()
    await connect()
    act(() => surface.type("echo hi\r"))
    expect(socket().sent[0]).toBeInstanceOf(Uint8Array)
    act(() => socket().onmessage?.({ data: new TextEncoder().encode("hi\r\n").buffer }))
    expect(surface.written).toContain("hi\r\n")
    act(() => surface.resize(120, 40))
    expect(socket().text()).toEqual(["echo hi\r", JSON.stringify({ type: "resize", cols: 120, rows: 40 })])
  })

  test.each([
    ["idle", "Closed after 30 minutes without input", "Closed: idle for 30 minutes", true],
    ["expired", "Your sign-in session ended; sign in again to open a terminal", "Closed: session expired", false],
    ["console.limit", "Too many open terminals; close one, then reconnect", "Closed: session limit", true],
    ["agent restarted", "The agent restarted; open a new terminal", "Closed: agent restarted", true],
    ["disconnected", "The agent restarted; open a new terminal", "Closed: agent restarted", true],
    ["revoked", "Your access to this host was revoked", "Closed: access revoked", false],
  ])("a close with reason %s shows %s, the state %s and Reconnect %s", async (reason, text, label, reconnect) => {
    render()
    await connect()
    act(() => socket().end(1000, reason))
    expect(await screen.findByRole("alert")).toHaveProperty("textContent", text)
    expect(screen.queryByRole("button", { name: "Reconnect" }) !== null).toBe(reconnect)
    expect(screen.getByText(label)).toBeTruthy()
  })

  test("Reconnect while connected drops the terminal and asks a code for a new one", async () => {
    render()
    const user = await connect()
    const first = socket()
    await user.click(screen.getByRole("button", { name: "Reconnect" }))
    expect(first.readyState).toBe(3)
    expect(await screen.findByLabelText("Authenticator code")).toBeTruthy()
  })

  test("the shell's exit code is named when the shell ends", async () => {
    render()
    await connect()
    act(() => socket().onmessage?.({ data: JSON.stringify({ type: "exit", code: 3 }) }))
    act(() => socket().end(1000, "ended"))
    expect((await screen.findByRole("alert")).textContent).toBe("The shell exited with code 3")
  })

  test("a host offline shows its text and Reconnect without asking a code", async () => {
    render({ online: false })
    expect((await screen.findByRole("alert")).textContent).toBe("The host is offline; reconnect once it is back")
    expect(screen.getByRole("button", { name: "Reconnect" })).toBeTruthy()
    expect(screen.queryByText(STEP_UP_REASON)).toBeNull()
  })

  test("an agent too old shows Update the agent and no Reconnect", async () => {
    render({ reported: false })
    expect((await screen.findByRole("alert")).textContent).toBe("Update the agent to use the terminal")
    expect(screen.queryByRole("button", { name: "Reconnect" })).toBeNull()
  })

  test("a socket refused before it opened says so; Reconnect asks a new code and opens a new socket", async () => {
    render()
    const user = await connect()
    act(() => socket().end(1000, "idle"))
    await user.click(await screen.findByRole("button", { name: "Reconnect" }))
    await user.type(await screen.findByLabelText("Authenticator code"), "654321")
    await user.click(screen.getByRole("button", { name: "Verify" }))
    await waitFor(() => expect(FakeSocket.instances).toHaveLength(2))
    act(() => socket().end(1006, ""))
    expect((await screen.findByRole("alert")).textContent).toBe("Couldn't open the terminal")
  })

  test("Close ends the terminal and says so", async () => {
    render()
    const user = await connect()
    await user.click(screen.getByRole("button", { name: "Close" }))
    expect((await screen.findByRole("alert")).textContent).toBe("You closed the terminal")
  })

  test("cancelling the code leaves the terminal closed with Reconnect", async () => {
    render()
    await userEvent.setup().click(await screen.findByRole("button", { name: "Cancel" }))
    expect((await screen.findByRole("alert")).textContent).toBe("The terminal was not opened")
    expect(FakeSocket.instances).toHaveLength(0)
  })
})

describe("focus shortcut and key bar", () => {
  test("Ctrl+Shift+L moves focus to the toolbar, whose text names the shortcut", async () => {
    render()
    await connect()
    const toolbar = screen.getByRole("toolbar", { name: "Terminal" })
    expect(toolbar.textContent).toContain("Ctrl+Shift+L moves focus here")
    const inner = document.createElement("textarea")
    screen.getByTestId("terminal-area").appendChild(inner)
    inner.focus()
    // fireEvent answers false when a handler prevented the default.
    expect(fireEvent.keyDown(inner, { key: "L", ctrlKey: true, shiftKey: true })).toBe(false)
    expect(document.activeElement).toBe(toolbar)
    inner.focus()
    // A plain Ctrl+L is left to xterm, which sends it to the shell.
    expect(fireEvent.keyDown(inner, { key: "l", ctrlKey: true })).toBe(true)
    expect(document.activeElement).toBe(inner)
  })

  test("below 768 px a key bar with Esc, Tab, Ctrl and arrows sends their sequences", async () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q === "(max-width: 767px)", media: q, addEventListener: () => {}, removeEventListener: () => {} }))
    render()
    const user = await connect()
    const keys = screen.getByRole("group", { name: "Terminal keys" })
    expect(Array.from(keys.querySelectorAll("button")).map((b) => b.getAttribute("aria-label") ?? b.textContent)).toEqual([
      "Esc",
      "Tab",
      "Ctrl",
      "Up arrow",
      "Down arrow",
      "Left arrow",
      "Right arrow",
    ])
    await user.click(screen.getByRole("button", { name: "Esc" }))
    await user.click(screen.getByRole("button", { name: "Tab" }))
    await user.click(screen.getByRole("button", { name: "Up arrow" }))
    await user.click(screen.getByRole("button", { name: "Ctrl" }))
    expect(screen.getByRole("button", { name: "Ctrl" }).getAttribute("aria-pressed")).toBe("true")
    act(() => surface.type("c"))
    expect(socket().text()).toEqual(["\x1b", "\t", "\x1b[A", "\x03"])
  })

  test("at 768 px and wider there is no key bar", async () => {
    render()
    await connect()
    expect(screen.queryByRole("group", { name: "Terminal keys" })).toBeNull()
  })
})
