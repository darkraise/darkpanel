import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, expect, test, vi } from "vitest"
import type { Host } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { ConsolePage } from "./console-page"
import type { TerminalSurface } from "./terminal"

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

const host: Host = {
  id: "h1", name: "web-1", scope: "admin", isLocal: false, state: "enrolled", online: true, lastSeenAt: null, agentVersion: "1.0.0",
  protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, ingestion: null,
  modules: [{ id: "console", version: 1, enabled: true, available: true, reason: null, checkedAt: null }],
}

afterEach(() => {
  vi.unstubAllGlobals()
})

// Mounting the terminal asks its code and opens a root shell, so a link that
// opens the files must not mount it before its tab is shown.
test("a ?path= link opens the files without asking the terminal's code; the Terminal tab then asks it", async () => {
  const calls = stubFetch({
    "POST /api/v1/actions/core.host.list/v1/read": { body: { output: { hosts: [host] } } },
    "GET /api/v1/hosts/h1/files/list": { body: { path: "/srv", entries: [] } },
    "POST /api/v1/hosts/h1/channels/console/terminal/ticket": { body: { ticket: "T1" } },
  })
  renderWithProviders(<ConsolePage hostId="h1" openSurface={() => surface} />, { path: "/?path=/srv" })
  await waitFor(() => expect(calls.some((c) => c.path.includes("/files/list"))).toBe(true))
  await new Promise((r) => setTimeout(r, 100))
  expect(screen.queryByLabelText("Authenticator code")).toBeNull()
  expect(calls.some((c) => c.path.endsWith("/terminal/ticket"))).toBe(false)
  await userEvent.setup().click(screen.getByRole("tab", { name: "Terminal" }))
  expect(await screen.findByLabelText("Authenticator code")).toBeTruthy()
})
