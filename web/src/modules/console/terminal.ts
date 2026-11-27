// TerminalSurface is what the terminal view draws into: xterm in the app,
// a fake in the unit tests (jsdom has no canvas).
export interface TerminalSurface {
  readonly cols: number
  readonly rows: number
  write: (data: Uint8Array | string) => void
  onData: (cb: (data: string) => void) => () => void
  onResize: (cb: (cols: number, rows: number) => void) => () => void
  fit: () => void
  focus: () => void
  dispose: () => void
}

export type OpenSurface = (el: HTMLElement) => TerminalSurface | Promise<TerminalSurface>

// The largest data message the bridge accepts (spec 1.2).
export const MAX_MESSAGE = 32 * 1024

export const FOCUS_SHORTCUT = "Ctrl+Shift+L"

// Closed is why a terminal is not open: its text, the short label the
// toolbar's state shows, and whether Reconnect can help.
export interface Closed {
  text: string
  label: string
  reconnect: boolean
}

export const HOST_OFFLINE: Closed = { text: "The host is offline; reconnect once it is back", label: "Closed: host offline", reconnect: true }
export const AGENT_TOO_OLD: Closed = { text: "Update the agent to use the terminal", label: "Closed: agent too old", reconnect: false }
export const NOT_OPENED: Closed = { text: "The terminal was not opened", label: "Closed: not opened", reconnect: true }
export const COULD_NOT_OPEN: Closed = { text: "Couldn't open the terminal", label: "Closed: couldn't open", reconnect: true }
export const CLOSED_BY_YOU: Closed = { text: "You closed the terminal", label: "Closed by you", reconnect: true }

// closedFor is what a close of the bridge with reason means; exit is the
// shell's exit code when the bridge sent one.
export function closedFor(reason: string, exit: number | null): Closed {
  switch (reason) {
    case "idle":
      return { text: "Closed after 30 minutes without input", label: "Closed: idle for 30 minutes", reconnect: true }
    case "expired":
      return { text: "Your sign-in session ended; sign in again to open a terminal", label: "Closed: session expired", reconnect: false }
    case "console.limit":
      return { text: "Too many open terminals; close one, then reconnect", label: "Closed: session limit", reconnect: true }
    case "agent restarted":
    case "disconnected":
      return { text: "The agent restarted; open a new terminal", label: "Closed: agent restarted", reconnect: true }
    case "revoked":
      return { text: "Your access to this host was revoked", label: "Closed: access revoked", reconnect: false }
    case "ended":
      return { text: exit === null ? "The shell exited" : `The shell exited with code ${exit}`, label: "Closed: shell exited", reconnect: true }
    case "":
      return exit === null ? { text: "The connection was lost", label: "Closed: connection lost", reconnect: true } : closedFor("ended", exit)
    default:
      return { text: `The terminal closed: ${reason}`, label: "Closed", reconnect: true }
  }
}

// KEYS are the key bar's keys and the sequences they send; Ctrl is a
// modifier for the next key typed.
export const KEYS: [string, string, string][] = [
  ["Esc", "Escape", "\x1b"],
  ["Tab", "Tab", "\t"],
  ["↑", "Up arrow", "\x1b[A"],
  ["↓", "Down arrow", "\x1b[B"],
  ["←", "Left arrow", "\x1b[D"],
  ["→", "Right arrow", "\x1b[C"],
]

// withCtrl is data typed after Ctrl: a letter becomes its control character.
export function withCtrl(data: string): string {
  const c = data.charAt(0).toLowerCase()
  if (data.length === 1 && c >= "a" && c <= "z") return String.fromCharCode(c.charCodeAt(0) - 96)
  return data
}

// terminalUrl is the bridge's WebSocket address on this page's origin.
export function terminalUrl(hostId: string, ticket: string, cols: number, rows: number): string {
  const scheme = window.location.protocol === "https:" ? "wss:" : "ws:"
  const q = new URLSearchParams({ ticket, cols: String(cols), rows: String(rows) })
  return `${scheme}//${window.location.host}/api/v1/hosts/${encodeURIComponent(hostId)}/channels/console/terminal?${q.toString()}`
}

// chunks splits bytes into messages the bridge accepts.
export function chunks(bytes: Uint8Array<ArrayBuffer>): Uint8Array<ArrayBuffer>[] {
  const out: Uint8Array<ArrayBuffer>[] = []
  for (let at = 0; at < bytes.length; at += MAX_MESSAGE) out.push(bytes.subarray(at, at + MAX_MESSAGE))
  return out
}
