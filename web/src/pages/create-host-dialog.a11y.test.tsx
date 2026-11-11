import { lazy } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Info } from "lucide-react"
import { expect, test } from "vitest"
import type { Change, Host, Plan } from "@/api/types"
import type { ModuleEntry } from "@/modules/types"
import { expectNoAxeViolations, focusedName, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { CreateHostDialog } from "./create-host-dialog"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const PLAN = "POST /api/v1/actions/core.host.create/v1/plan"
const APPLY = "POST /api/v1/actions/core.host.create/v1/apply"

function host(modules: Host["modules"]): Host {
  return { id: "h1", name: "controller", isLocal: true, state: "enrolled", online: true, lastSeenAt: "2026-11-11T08:00:00Z", agentVersion: "1.0.0", protocolVersion: 1, os: "debian-12", arch: "amd64", supported: true, modules, ingestion: null }
}

const plan: Plan = {
  planId: "p1", hostId: null, action: "core.host.create", version: 1, inputHash: "h",
  diff: [{ op: "+", text: "host web-2: pending" }], guards: [{ id: "name-free", ok: true, message: "no host is named web-2", overridable: false, overridden: false }],
  expiresAt: "2026-10-23T09:00:00Z", confirmation: null,
}

function change(output: unknown): Change {
  return {
    id: "c1", hostId: null, action: "core.host.create", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-23T08:00:00Z", updatedAt: "2026-10-23T08:00:00Z", revertDeadline: null, output, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
}

const grant = { host: { id: "h2", name: "web-2" }, token: "dpe_example_enrollment", expiresAt: "2026-10-23T09:00:00Z" }

const tailnet = { id: "tailnet", version: 1, enabled: true, available: true, reason: null, checkedAt: null }

// A module input with a field of its own, so the form's keyboard order covers an extension.
const joinInput = lazy(async () => ({
  default: () => (
    <div>
      <label htmlFor="cha-extra">Extra input</label>
      <input id="cha-extra" />
    </div>
  ),
}))
const entries: ModuleEntry[] = [
  { id: "cha", label: "Extra", icon: Info, routes: lazy(async () => ({ default: () => null })), hostCreate: { id: "cha.extra", render: joinInput } },
]
const extra = { id: "cha", version: 1, enabled: true, available: true, reason: null, checkedAt: null }

async function openForm(modules: Host["modules"] = [], answer: unknown = grant, entriesOf?: ModuleEntry[]) {
  stubFetch({
    [HOSTS]: { body: { output: { hosts: [host(modules)] } } },
    [PLAN]: { body: plan },
    [APPLY]: { body: change(answer) },
  })
  const view = renderWithProviders(entriesOf === undefined ? <CreateHostDialog /> : <CreateHostDialog entries={entriesOf} />)
  const user = userEvent.setup()
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(false))
  await user.click(screen.getByRole("button", { name: "Add host" }))
  return { user, dialog: await screen.findByRole("dialog", { name: "Add a host" }), ...view }
}

async function toGrant(answer: unknown) {
  const opened = await openForm([], answer)
  await opened.user.type(within(opened.dialog).getByLabelText("Name"), "web-2")
  await opened.user.click(within(opened.dialog).getByRole("button", { name: "Continue" }))
  const review = await screen.findByRole("dialog", { name: "Add a host" })
  await opened.user.click(await within(review).findByRole("button", { name: "Apply" }))
  return { ...opened, done: await screen.findByRole("dialog", { name: "Host added" }) }
}

test("the Add host form passes axe, also with the tailnet input and its tag hint", async () => {
  const { user, dialog } = await openForm([tailnet])
  await expectNoAxeViolations(dialog)
  await user.click(await within(dialog).findByRole("checkbox", { name: "Join the tailnet" }))
  await user.type(within(dialog).getByLabelText("Tags (comma-separated)"), "server")
  expect(within(dialog).getByText("Each tag looks like tag:server.")).toBeTruthy()
  await expectNoAxeViolations(dialog)
})

test("the plan, the enrollment token and the missing-token notice pass axe", async () => {
  const opened = await openForm()
  await opened.user.type(within(opened.dialog).getByLabelText("Name"), "web-2")
  await opened.user.click(within(opened.dialog).getByRole("button", { name: "Continue" }))
  const review = await screen.findByRole("dialog", { name: "Add a host" })
  await within(review).findByLabelText("Planned changes")
  await expectNoAxeViolations(review)
  await opened.user.click(within(review).getByRole("button", { name: "Apply" }))
  await expectNoAxeViolations(await screen.findByRole("dialog", { name: "Host added" }))
  opened.unmount()

  const missing = await toGrant({ host: { id: "h2", name: "web-2" } })
  expect(within(missing.done).getByRole("alert")).toBeTruthy()
  await expectNoAxeViolations(missing.done)
})

test("the Add host button passes axe while the host list loads", async () => {
  stubFetch({ [HOSTS]: () => new Promise(() => undefined) })
  const { container } = renderWithProviders(<CreateHostDialog />)
  expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(true)
  await expectNoAxeViolations(container)
})

test("the keyboard moves through the form in reading order and the dialog holds the focus", async () => {
  const { user, dialog } = await openForm([extra], grant, entries)
  await within(dialog).findByLabelText("Extra input")
  expect(focusedName()).toBe("Name")
  expect(await tabSequence(user, 5)).toEqual(["Controller address (optional)", "Extra input", "Continue", "Close", "Name"])
  expect(await tabSequence(user, 2, { backwards: true })).toEqual(["Close", "Continue"])
  expect(dialog.contains(document.activeElement)).toBe(true)
})

test("Escape closes the form and the focus returns to the Add host button", async () => {
  const { user } = await openForm()
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})

test("Escape closes the plan dialog and the focus returns to the Add host button", async () => {
  const { user, dialog } = await openForm()
  await user.type(within(dialog).getByLabelText("Name"), "web-2")
  await user.click(within(dialog).getByRole("button", { name: "Continue" }))
  const review = await screen.findByRole("dialog", { name: "Add a host" })
  await within(review).findByLabelText("Planned changes")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})

test("Done closes the enrollment token dialog and the focus returns to the Add host button", async () => {
  const { user, done } = await toGrant(grant)
  expect(within(done).getByDisplayValue("dpe_example_enrollment")).toBeTruthy()
  await user.click(within(done).getByRole("button", { name: "Done" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})

test("Escape closes the enrollment token dialog and the focus returns to the Add host button", async () => {
  const { user } = await toGrant(grant)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(focusedName()).toBe("Add host")
})
