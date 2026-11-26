import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, test, vi } from "vitest"
import { filesDefault, filesWorst } from "@/modules/m11-fixtures"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { CONFLICT, NOT_TEXT } from "./files"
import { FilesView } from "./files-view"

const FILES = "/api/v1/hosts/h1/files"
const THEMES = ["light", "dark"] as const
const problem = (status: number, code: string): StubHandler => ({ status, body: { type: "about:blank", title: code, status, detail: code, code } })
const hosts: StubHandler = { body: "127.0.0.1 localhost\n", headers: { "Content-Type": "text/plain; charset=utf-8", "Darkpanel-Sha256": "s" } }
const worst = { ...filesWorst, entries: filesWorst.entries.slice(0, 40) }
const protectedDir = { path: "/etc/darkpanel", protected: "Darkpanel never changes files under /etc/darkpanel", entries: [] }

async function edit(name: string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }))
  await user.click(screen.getByRole("menuitem", { name: /^Edit/ }))
  return user
}

class SilentXhr {
  upload = { onprogress: null }
  open() {}
  setRequestHeader() {}
  getResponseHeader() {
    return null
  }
  send() {}
  abort() {}
}

// Each state: the list answer, the read and write answers, the path and how
// to reach the state.
const STATES: [string, StubHandler, StubHandler, StubHandler, string, () => Promise<void>][] = [
  ["loading", () => new Promise(() => {}), hosts, hosts, "/etc", async () => {
    await screen.findByRole("status", { name: "Loading /etc" })
  }],
  ["error", problem(500, "internal"), hosts, hosts, "/etc", async () => {
    await screen.findByText("Couldn't list /etc")
  }],
  ["files", { body: filesDefault }, hosts, hosts, "/etc", async () => {
    await screen.findByRole("table", { name: "Files in /etc" })
  }],
  ["worst case", { body: worst }, hosts, hosts, "/srv/uploads", async () => {
    await screen.findByRole("button", { name: "Show more" })
  }],
  ["protected folder", { body: protectedDir }, hosts, hosts, "/etc/darkpanel", async () => {
    await screen.findByText("Darkpanel never changes files under /etc/darkpanel")
  }],
  ["protected row menu", { body: filesDefault }, hosts, hosts, "/etc", async () => {
    await userEvent.setup().click(await screen.findByRole("button", { name: "Actions for darkpanel" }))
    await screen.findByRole("menu")
  }],
  ["delete a folder", { body: filesDefault }, hosts, hosts, "/etc", async () => {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Actions for nginx" }))
    await user.click(screen.getByRole("menuitem", { name: /^Delete/ }))
    await user.click(await screen.findByRole("button", { name: "Delete" }))
    await screen.findByText("Type the path exactly")
  }],
  ["editor", { body: filesDefault }, hosts, hosts, "/etc", async () => {
    await edit("hosts")
    await screen.findByLabelText("/etc/hosts")
  }],
  ["not text", { body: filesDefault }, problem(422, "files.not_text"), hosts, "/etc", async () => {
    await edit("hosts")
    await screen.findByText(NOT_TEXT)
  }],
  ["conflict", { body: filesDefault }, hosts, problem(409, "files.conflict"), "/etc", async () => {
    const user = await edit("hosts")
    await user.click(await screen.findByRole("button", { name: "Save" }))
    await screen.findByText(CONFLICT)
  }],
  ["upload progress", { body: filesDefault }, problem(422, "files.not_text"), hosts, "/etc", async () => {
    vi.stubGlobal("XMLHttpRequest", SilentXhr)
    await screen.findByRole("table", { name: "Files in /etc" })
    await userEvent.setup().upload(screen.getByTestId("upload-picker"), new File(["x"], "backup.tar.gz"))
    await waitFor(() => screen.getByRole("progressbar", { name: "Upload progress" }))
  }],
]

afterEach(() => {
  document.documentElement.classList.remove("dark")
  vi.unstubAllGlobals()
})

describe("no axe violations in any state", () => {
  for (const theme of THEMES) {
    test.each(STATES)(`%s (${theme})`, async (_name, list, read, write, path, reach) => {
      document.documentElement.classList.toggle("dark", theme === "dark")
      stubFetch({ [`GET ${FILES}/list`]: list, [`GET ${FILES}/read`]: read, [`POST ${FILES}/write`]: write })
      const { container } = renderWithProviders(<FilesView hostId="h1" />, { path: `/?path=${path}` })
      await reach()
      await expectNoAxeViolations(container)
      for (const layer of document.querySelectorAll("[role=dialog], [role=menu]")) await expectNoAxeViolations(layer)
    }, 20_000)
  }
})
