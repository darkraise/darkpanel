import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { useNavigate } from "react-router"
import { afterEach, describe, expect, test, vi } from "vitest"
import { filesDefault, filesWorst } from "@/modules/m11-fixtures"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { CONFLICT, ESCAPED_REASON, NOT_TEXT } from "./files"
import { FilesView } from "./files-view"

const FILES = "/api/v1/hosts/h1/files"
const problem = (status: number, code: string, detail = code) => ({ status, body: { type: "about:blank", title: detail, status, detail, code } })
const text = (body: string, sha: string) => ({ body, headers: { "Content-Type": "text/plain; charset=utf-8", "Darkpanel-Sha256": sha } })
const query = (c: FetchCall) => new URL(c.path, "http://localhost").searchParams

function Back() {
  const navigate = useNavigate()
  return (
    <button type="button" onClick={() => void navigate(-1)}>
      Browser back
    </button>
  )
}

function GoRoot() {
  const navigate = useNavigate()
  return (
    <button type="button" onClick={() => void navigate("/?path=/")}>
      Go to the root
    </button>
  )
}

// listing answers each path from dirs, and a second page for a cursor.
function listing(dirs: Record<string, object>, more?: object): StubHandler {
  return (c) => {
    const q = query(c)
    if (q.has("cursor") && more !== undefined) return { body: more }
    return { body: dirs[q.get("path") ?? ""] ?? { path: q.get("path"), entries: [] } }
  }
}

function render(routes: Record<string, StubHandler>, path = "/etc") {
  const calls = stubFetch({ [`GET ${FILES}/list`]: listing({ "/etc": filesDefault }), ...routes })
  renderWithProviders(
    <>
      <FilesView hostId="h1" />
      <Back />
      <GoRoot />
    </>,
    { path: `/?path=${path}` },
  )
  return calls
}

async function menuOf(name: string) {
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: `Actions for ${name}` }))
  return user
}

function item(name: string): HTMLElement {
  return screen.getByRole("menuitem", { name: new RegExp(`^${name}`) })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("navigation and states", () => {
  test("the path lives in ?path= and back restores the previous directory", async () => {
    const calls = render({})
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "nginx/" }))
    expect(screen.getByTestId("location").textContent).toBe("/?path=%2Fetc%2Fnginx")
    expect(await screen.findByRole("table", { name: "Files in /etc/nginx" })).toBeTruthy()
    expect(calls.map((c) => query(c).get("path"))).toEqual(["/etc", "/etc/nginx"])
    expect(screen.getByLabelText("Path")).toHaveProperty("value", "/etc/nginx")
    await user.click(screen.getByRole("button", { name: "Browser back" }))
    expect(await screen.findByRole("table", { name: "Files in /etc" })).toBeTruthy()
    expect(screen.getByLabelText("Path")).toHaveProperty("value", "/etc")
    await user.clear(screen.getByLabelText("Path"))
    await user.type(screen.getByLabelText("Path"), "/var//log/{Enter}")
    expect(screen.getByTestId("location").textContent).toBe("/?path=%2Fvar%2Flog")
    await user.click(await screen.findByRole("button", { name: "/" }))
    expect(screen.getByTestId("location").textContent).toBe("/?path=%2F")
  })

  test("2000 entries render with Show more, which asks the next page by its cursor", async () => {
    const more = { path: "/srv/uploads", entries: [{ ...filesDefault.entries[3], name: "zz-last.txt" }] }
    const calls = render({ [`GET ${FILES}/list`]: listing({ "/srv/uploads": filesWorst }, more) }, "/srv/uploads")
    const table = await screen.findByRole("table", { name: "Files in /srv/uploads" })
    expect(within(table).getAllByRole("button", { name: /^Actions for / })).toHaveLength(2000)
    expect(screen.getByText("2000 entries shown; the folder holds more")).toBeTruthy()
    await userEvent.setup().click(screen.getByRole("button", { name: "Show more" }))
    expect(await screen.findByText("zz-last.txt")).toBeTruthy()
    expect(within(table).getAllByRole("button", { name: /^Actions for / })).toHaveLength(2001)
    expect(query(calls[calls.length - 1] as FetchCall).get("cursor")).toBe("cursor-2000")
    expect(screen.queryByRole("button", { name: "Show more" })).toBeNull()
  }, 30_000)

  test("a 255-byte name and an escaped non-UTF-8 name wrap, and the escaped one allows no change", async () => {
    const long = filesWorst.entries[1]
    const escaped = filesWorst.entries[7]
    if (long === undefined || escaped === undefined) throw new Error("fixture changed")
    expect(new TextEncoder().encode(long.name).length).toBe(255)
    render({ [`GET ${FILES}/list`]: listing({ "/srv/uploads": { path: "/srv/uploads", entries: [long, escaped] } }) }, "/srv/uploads")
    const longCell = (await screen.findByText(long.name)).closest("td")
    const escapedCell = screen.getByText(escaped.name).closest("td")
    expect(longCell?.className).toContain("break-all")
    expect(escapedCell?.className).toContain("break-all")
    expect(escaped.name).toContain("\\xff")
    await menuOf(escaped.name)
    for (const name of ["Download", "Edit", "Rename", "Permissions", "Delete"]) {
      expect(item(name).getAttribute("aria-disabled")).toBe("true")
      expect(item(name).textContent).toContain(ESCAPED_REASON)
    }
  })

  test("a protected path shows its write actions disabled with the reason", async () => {
    render({
      [`GET ${FILES}/list`]: listing({ "/etc": filesDefault, "/etc/darkpanel": { path: "/etc/darkpanel", protected: "Darkpanel never changes files under /etc/darkpanel", entries: [] } }),
    })
    const reason = filesDefault.entries[2]?.protected ?? ""
    await menuOf("darkpanel")
    for (const name of ["Rename", "Permissions", "Delete"]) {
      expect(item(name).getAttribute("aria-disabled")).toBe("true")
      expect(item(name).textContent).toContain(reason)
    }
    await userEvent.setup().keyboard("{Escape}")
    await userEvent.setup().click(screen.getByRole("button", { name: "darkpanel/" }))
    expect(await screen.findByText("Darkpanel never changes files under /etc/darkpanel")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Upload" })).toHaveProperty("disabled", true)
    expect(screen.getByRole("button", { name: "New folder" })).toHaveProperty("disabled", true)
  })

  test("a non-text file, and one over 1 MiB without a read, show Download it instead", async () => {
    const big = { ...filesDefault.entries[3], name: "big.log", size: 2 << 20 }
    const calls = render({
      [`GET ${FILES}/list`]: listing({ "/etc": { path: "/etc", entries: [...filesDefault.entries, big] } }),
      [`GET ${FILES}/read`]: problem(422, "files.not_text", "Not a text file"),
    })
    let user = await menuOf("hosts")
    await user.click(item("Edit"))
    expect(await screen.findByText(NOT_TEXT)).toBeTruthy()
    expect(screen.getByRole("button", { name: "Download" })).toBeTruthy()
    await user.click(screen.getByRole("button", { name: "Close" }))
    const reads = calls.filter((c) => c.path.includes("/files/read")).length
    user = await menuOf("big.log")
    await user.click(item("Edit"))
    expect(await screen.findByText(NOT_TEXT)).toBeTruthy()
    expect(calls.filter((c) => c.path.includes("/files/read"))).toHaveLength(reads)
  })

  test("a save answering files.conflict shows Reload and Overwrite; Overwrite saves over the current hash", async () => {
    let reads = 0
    const calls = render({
      [`GET ${FILES}/read`]: () => (reads++ === 0 ? text("127.0.0.1 localhost\n", "sha-opened") : text("changed\n", "sha-now")),
      [`POST ${FILES}/write`]: (c) => (query(c).get("sha256") === "sha-opened" ? problem(409, "files.conflict", "Changed on the host") : { body: { sha256: "sha-new", size: 9 } }),
    })
    const user = await menuOf("hosts")
    await user.click(item("Edit"))
    const area = await screen.findByLabelText("/etc/hosts")
    expect(area).toHaveProperty("value", "127.0.0.1 localhost\n")
    await user.type(area, "::1 x")
    await user.click(screen.getByRole("button", { name: "Save" }))
    const alert = await screen.findByRole("alert")
    expect(within(alert).getByText(CONFLICT)).toBeTruthy()
    expect(within(alert).getByRole("button", { name: "Reload" })).toBeTruthy()
    await user.click(within(alert).getByRole("button", { name: "Overwrite" }))
    await waitFor(() => expect(screen.queryByText(CONFLICT)).toBeNull())
    const writes = calls.filter((c) => c.path.includes("/files/write"))
    expect(writes.map((c) => query(c).get("sha256"))).toEqual(["sha-opened", "sha-now"])
    expect(query(writes[1] as FetchCall).get("path")).toBe("/etc/hosts")
    expect(writes[1]?.headers.get("Content-Type")).toBe("application/octet-stream")
    expect(await (writes[1]?.raw as Blob).text()).toBe("127.0.0.1 localhost\n::1 x")
    expect(screen.getByLabelText("/etc/hosts")).toHaveProperty("value", "127.0.0.1 localhost\n::1 x")
  })

  test("Reload asks before it drops the edits, then reads the file again", async () => {
    let reads = 0
    render({
      [`GET ${FILES}/read`]: () => (reads++ === 0 ? text("old\n", "a") : text("new\n", "b")),
      [`POST ${FILES}/write`]: problem(409, "files.conflict", "Changed on the host"),
    })
    const user = await menuOf("hosts")
    await user.click(item("Edit"))
    await user.type(await screen.findByLabelText("/etc/hosts"), "mine")
    await user.click(screen.getByRole("button", { name: "Save" }))
    await user.click(await screen.findByRole("button", { name: "Reload" }))
    expect(screen.getByText("Discard your changes to /etc/hosts?")).toBeTruthy()
    await user.click(screen.getByRole("button", { name: "Discard changes" }))
    await waitFor(() => expect(screen.getByLabelText("/etc/hosts")).toHaveProperty("value", "new\n"))
    expect(screen.queryByText(CONFLICT)).toBeNull()
  })

  test("Close and leaving the folder ask before unsaved text is dropped", async () => {
    render({ [`GET ${FILES}/read`]: text("old\n", "a") })
    const user = await menuOf("hosts")
    await user.click(item("Edit"))
    await user.type(await screen.findByLabelText("/etc/hosts"), "mine")
    await user.click(screen.getByRole("button", { name: "Close" }))
    await user.click(screen.getByRole("button", { name: "Keep editing" }))
    expect(screen.getByLabelText("/etc/hosts")).toHaveProperty("value", "old\nmine")
    // A link elsewhere leaves /etc while the text is unsaved.
    await user.click(screen.getByRole("button", { name: "Go to the root" }))
    expect(await screen.findByText("Discard your changes to /etc/hosts?")).toBeTruthy()
    await user.click(screen.getByRole("button", { name: "Keep editing" }))
    await waitFor(() => expect(screen.queryByText("Discard your changes to /etc/hosts?")).toBeNull())
    expect(screen.getByLabelText("/etc/hosts")).toHaveProperty("value", "old\nmine")
    await user.click(screen.getByRole("button", { name: "Close" }))
    await user.click(screen.getByRole("button", { name: "Discard changes" }))
    expect(await screen.findByRole("button", { name: "Actions for hosts" })).toBeTruthy()
  })

  test("deleting a directory asks its path; a file only a confirmation", async () => {
    const calls = render({ [`POST ${FILES}/delete`]: { status: 204 } })
    let user = await menuOf("nginx")
    await user.click(item("Delete"))
    const dialog = await screen.findByRole("dialog", { name: "Delete nginx and everything in it" })
    await user.type(within(dialog).getByLabelText("Path"), "/etc/ngin")
    await user.click(within(dialog).getByRole("button", { name: "Delete" }))
    expect(within(dialog).getByText("Type the path exactly")).toBeTruthy()
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0)
    await user.type(within(dialog).getByLabelText("Path"), "x")
    await user.click(within(dialog).getByRole("button", { name: "Delete" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    user = await menuOf("fstab")
    await user.click(item("Delete"))
    const fileDialog = await screen.findByRole("dialog", { name: "Delete fstab" })
    expect(within(fileDialog).queryByLabelText("Path")).toBeNull()
    await user.click(within(fileDialog).getByRole("button", { name: "Delete" }))
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(calls.filter((c) => c.method === "POST").map((c) => c.body)).toEqual([
      { path: "/etc/nginx", confirm: "/etc/nginx" },
      { path: "/etc/fstab" },
    ])
  })

  test("rename, permissions and new folder send their changes", async () => {
    const calls = render({ [`POST ${FILES}/rename`]: { status: 204 }, [`POST ${FILES}/chmod`]: { status: 204 }, [`POST ${FILES}/mkdir`]: { status: 204 } })
    let user = await menuOf("hosts")
    await user.click(item("Rename"))
    const rename = await screen.findByRole("dialog", { name: "Rename hosts" })
    await user.clear(within(rename).getByLabelText("New name"))
    await user.type(within(rename).getByLabelText("New name"), "a/b")
    expect(within(rename).getByText("A name cannot hold /")).toBeTruthy()
    await user.clear(within(rename).getByLabelText("New name"))
    await user.type(within(rename).getByLabelText("New name"), "hosts.old{Enter}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    user = await menuOf("hosts")
    await user.click(item("Permissions"))
    const mode = await screen.findByRole("dialog", { name: "Permissions of hosts" })
    await user.clear(within(mode).getByLabelText("Mode"))
    await user.type(within(mode).getByLabelText("Mode"), "0900")
    expect(within(mode).getByText("Enter 3 or 4 octal digits")).toBeTruthy()
    await user.clear(within(mode).getByLabelText("Mode"))
    await user.type(within(mode).getByLabelText("Mode"), "0600{Enter}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await user.click(screen.getByRole("button", { name: "New folder" }))
    await user.type(within(await screen.findByRole("dialog", { name: "New folder" })).getByLabelText("Folder name"), "sites{Enter}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(calls.filter((c) => c.method === "POST").map((c) => [c.path, c.body])).toEqual([
      [`${FILES}/rename`, { path: "/etc/hosts", to: "/etc/hosts.old" }],
      [`${FILES}/chmod`, { path: "/etc/hosts", mode: "0600" }],
      [`${FILES}/mkdir`, { path: "/etc/sites" }],
    ])
  })

  test("upload shows progress and Cancel, which aborts it", async () => {
    const sent: FakeXhr[] = []
    class FakeXhr {
      upload: { onprogress: ((e: { loaded: number }) => void) | null } = { onprogress: null }
      onload: (() => void) | null = null
      onerror: (() => void) | null = null
      onabort: (() => void) | null = null
      status = 0
      responseText = ""
      url = ""
      headers: Record<string, string> = {}
      body: unknown = null
      open(_method: string, url: string) {
        this.url = url
      }
      setRequestHeader(k: string, v: string) {
        this.headers[k] = v
      }
      getResponseHeader() {
        return null
      }
      send(body: unknown) {
        this.body = body
        sent.push(this)
      }
      abort = vi.fn(() => this.onabort?.())
    }
    vi.stubGlobal("XMLHttpRequest", FakeXhr)
    render({ [`GET ${FILES}/read`]: problem(422, "files.not_text", "Not a text file") })
    await screen.findByRole("table", { name: "Files in /etc" })
    const file = new File([new Uint8Array(100)], "backup.tar.gz")
    await userEvent.setup().upload(screen.getByTestId("upload-picker"), file)
    await waitFor(() => expect(sent).toHaveLength(1))
    const xhr = sent[0] as FakeXhr
    expect(new URL(xhr.url, "http://localhost").searchParams.get("path")).toBe("/etc/backup.tar.gz")
    expect(xhr.headers["Content-Type"]).toBe("application/octet-stream")
    expect(xhr.body).toBe(file)
    expect(screen.getByText("Uploading backup.tar.gz to /etc")).toBeTruthy()
    xhr.upload.onprogress?.({ loaded: 50 })
    await waitFor(() => expect(screen.getByRole("progressbar", { name: "Upload progress" }).getAttribute("aria-valuenow")).toBe("50"))
    expect(screen.getByText("50 B of 100 B · 50%")).toBeTruthy()
    expect(screen.getByRole("button", { name: "Upload" })).toHaveProperty("disabled", true)
    await userEvent.setup().click(screen.getByRole("button", { name: "Cancel" }))
    expect(xhr.abort).toHaveBeenCalled()
    await waitFor(() => expect(screen.queryByRole("progressbar")).toBeNull())
  })

  test("opening a file outside the step-up window asks a code once, then opens it", async () => {
    let elevated = false
    const calls = render({
      [`GET ${FILES}/read`]: () => (elevated ? text("x\n", "s") : problem(403, "auth.step_up_required", "enter an authenticator code")),
      "POST /api/v1/auth/step-up": () => {
        elevated = true
        return { status: 204 }
      },
    })
    const user = await menuOf("hosts")
    await user.click(item("Edit"))
    const dialog = await screen.findByRole("dialog", { name: "Enter your authenticator code" })
    await user.type(within(dialog).getByLabelText("Authenticator code"), "123456")
    await user.click(within(dialog).getByRole("button", { name: "Verify" }))
    await waitFor(() => expect(screen.getByLabelText("/etc/hosts")).toHaveProperty("value", "x\n"))
    expect(calls.filter((c) => c.path === "/api/v1/auth/step-up")).toHaveLength(1)
  })
})
