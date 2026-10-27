import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { formatTime } from "@/lib/format"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { fwSnapshot } from "./fixtures"
import { SnapshotsPanel } from "./snapshots-panel"

const COMPARE = "POST /api/v1/hosts/h1/actions/firewall.snapshot.compare/v1/read"
const OLD = "2026-10-20T08:00:00Z"
const MID = "2026-10-22T08:00:00Z"
const NEW = "2026-10-24T08:00:00Z"

const scmpOutput = {
  from: "s-mid",
  to: "now",
  diff: [{ op: "+", text: "allow in tcp to any port 443" }, { op: "-", text: "deny in tcp to any port 23" }],
  files: [{ path: "/etc/ufw/user.rules", from: "sha256:aa", to: "sha256:bb" }, { path: "/etc/ufw/user6.rules", from: "sha256:cc", to: "sha256:dd" }],
}

function scmpRender(compare: StubHandler = { body: { output: scmpOutput } }): FetchCall[] {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/resources/firewall/snapshots": {
      body: { snapshots: [fwSnapshot({ id: "s-old", createdAt: OLD }), fwSnapshot({ id: "s-new", createdAt: NEW }), fwSnapshot({ id: "s-mid", createdAt: MID })] },
    },
    [COMPARE]: compare,
  })
  renderWithProviders(<SnapshotsPanel hostId="h1" />)
  return calls
}

const scmpBox = (iso: string) => screen.findByRole("checkbox", { name: `Select the snapshot taken ${formatTime(iso)}` })

test("one selected snapshot is compared with now", async () => {
  const calls = scmpRender()
  const user = userEvent.setup()
  expect((await screen.findByRole("button", { name: "Compare with now" })).hasAttribute("disabled")).toBe(true)
  await user.click(await scmpBox(MID))
  await user.click(screen.getByRole("button", { name: "Compare with now" }))
  const dialog = await screen.findByRole("dialog", { name: "Compare with now" })
  expect((await within(dialog).findByLabelText("Differences")).textContent).toBe("+ allow in tcp to any port 443- deny in tcp to any port 23")
  expect(within(within(dialog).getByRole("list", { name: "Files that differ" })).getAllByRole("listitem").map((li) => li.textContent)).toEqual([
    "/etc/ufw/user.rules",
    "/etc/ufw/user6.rules",
  ])
  expect(calls.find((c) => c.path.endsWith("/read"))?.body).toEqual({ input: { from: "s-mid" } })
})

test("two selected snapshots are compared older to newer, and a third replaces the first", async () => {
  const calls = scmpRender()
  const user = userEvent.setup()
  await user.click(await scmpBox(NEW))
  await user.click(await scmpBox(OLD))
  await user.click(await scmpBox(MID))
  expect(screen.getByRole("checkbox", { name: `Select the snapshot taken ${formatTime(NEW)}`, checked: false })).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Compare the two" }))
  const dialog = await screen.findByRole("dialog", { name: "Compare two snapshots" })
  await within(dialog).findByLabelText("Differences")
  expect(calls.find((c) => c.path.endsWith("/read"))?.body).toEqual({ input: { from: "s-old", to: "s-mid" } })
})

test("no differences says so", async () => {
  scmpRender({ body: { output: { from: "s-old", to: "now", diff: [], files: [] } } })
  const user = userEvent.setup()
  await user.click(await scmpBox(OLD))
  await user.click(screen.getByRole("button", { name: "Compare with now" }))
  expect((await within(await screen.findByRole("dialog")).findByText("No differences.")).textContent).toBe("No differences.")
})

test("a refused compare shows the server's reason", async () => {
  scmpRender({ status: 404, body: { type: "about:blank", title: "Not found", status: 404, detail: "no snapshot s-old", code: "request.invalid_input" } })
  const user = userEvent.setup()
  await user.click(await scmpBox(OLD))
  await user.click(screen.getByRole("button", { name: "Compare with now" }))
  expect((await within(await screen.findByRole("dialog")).findByRole("alert")).textContent).toBe("no snapshot s-old")
})

test("the comparison has no accessibility violations", async () => {
  scmpRender()
  const user = userEvent.setup()
  await user.click(await scmpBox(OLD))
  await user.click(screen.getByRole("button", { name: "Compare with now" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Differences")
  expect(await axe(dialog)).toHaveNoViolations()
})
