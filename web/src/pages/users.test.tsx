import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { DirectoryUser, HostGroup } from "@/api/types"
import { makeChange, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { UsersPage } from "./users"

const route = (action: string, op: string) => `POST /api/v1/actions/${action}/v1/${op}`

function dirUser(over: Partial<DirectoryUser>): DirectoryUser {
  return {
    id: `u-${over.username ?? "x"}`, username: "x", role: "member", state: "active", createdAt: "2026-11-20T08:00:00Z", disabled: false,
    grants: [], email: "", tailnetLogin: "", signIn: ["password"], ...over,
  }
}

const groups: HostGroup[] = [
  { id: "g1", name: "web", hostIds: ["h1"], hostCount: 1, grantCount: 1, createdAt: "2026-11-20T08:00:00Z" },
  { id: "g2", name: "db", hostIds: ["h2"], hostCount: 1, grantCount: 1, createdAt: "2026-11-20T08:00:00Z" },
]

const admin = dirUser({ username: "admin", role: "admin" })

function stub(users: () => DirectoryUser[], extra: Parameters<typeof stubFetch>[0] = {}) {
  return stubFetch({
    [route("core.user.directory", "read")]: () => ({ body: { output: { users: users() } } }),
    [route("core.group.list", "read")]: { body: { output: { groups } } },
    ...extra,
  })
}

test("UX-1 invite flow: the form plans core.user.invite, the link shows once, focus returns to the new row", async () => {
  let invited = false
  const noor = dirUser({ username: "noor", state: "pending", signIn: [], grants: [{ role: "operator", target: "g1" }] })
  const calls = stub(() => (invited ? [admin, noor] : [admin]), {
    [route("core.user.invite", "plan")]: { body: makePlan("core.user.invite", { confirmation: "noor" }) },
    [route("core.user.invite", "apply")]: () => {
      invited = true
      return { body: makeChange("core.user.invite", { user: noor, token: "tok-123", expiresAt: "2026-11-26T09:40:00Z" }) }
    },
  })
  renderWithProviders(<UsersPage />)
  const user = userEvent.setup()
  await screen.findByText("admin")
  await user.click(screen.getByRole("button", { name: "Invite user" }))
  const form = await screen.findByRole("dialog", { name: "Invite user" })
  await user.type(within(form).getByLabelText("Username"), "noor")
  await user.click(await within(form).findByRole("checkbox", { name: "web" }))
  await user.click(within(within(form).getByRole("radiogroup", { name: "Role on web" })).getByRole("radio", { name: "Operator" }))
  await user.type(within(form).getByLabelText("Email (optional)"), "noor@example.com")
  await user.type(within(form).getByLabelText("Tailnet login (optional)"), "noor@github")
  await user.click(within(form).getByRole("button", { name: "Review invite" }))

  const change = await screen.findByRole("dialog", { name: "Invite noor" })
  await user.type(await within(change).findByLabelText("Type noor to confirm"), "noor")
  await user.click(within(change).getByRole("button", { name: "Apply" }))
  expect(calls.find((c) => c.path.endsWith("invite/v1/plan"))?.body).toEqual({
    input: { username: "noor", role: "member", grants: [{ role: "operator", target: "g1" }], email: "noor@example.com", tailnetLogin: "noor@github" },
  })

  const link = await screen.findByRole("dialog", { name: "Enrollment link for noor" })
  expect((within(link).getByLabelText("Enrollment link") as HTMLInputElement).value).toBe(`${window.location.origin}/enroll#tok-123`)
  expect(within(link).getByRole("button", { name: "Copy" })).toBeTruthy()
  expect(within(link).getByText(/^Expires /)).toBeTruthy()
  await user.click(within(link).getByRole("button", { name: "Done" }))
  await waitFor(() => expect(document.activeElement?.getAttribute("data-username")).toBe("noor"))
  expect(screen.queryByText(/tok-123/)).toBeNull()
})

test("an admin invite sends no grants and hides the access list", async () => {
  stub(() => [admin])
  renderWithProviders(<UsersPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Invite user" }))
  const form = await screen.findByRole("dialog", { name: "Invite user" })
  await user.click(within(form).getByRole("radio", { name: "Admin" }))
  expect(within(form).queryByRole("group", { name: "Access" })).toBeNull()
  await user.click(within(form).getByRole("button", { name: "Review invite" }))
  expect(await within(form).findByText(/A username is 1 to 64/)).toBeTruthy()
})

test("UI-1 row menu and access column", async () => {
  stub(() => [
    admin,
    dirUser({ username: "mia", grants: [{ role: "operator", target: "g1" }, { role: "operator", target: "g2" }], signIn: ["password", "oidc"] }),
    dirUser({ username: "sam", grants: [{ role: "viewer", target: "*" }], signIn: ["tailscale"] }),
    dirUser({ username: "noor", state: "pending", signIn: [] }),
    dirUser({ username: "ola", disabled: true }),
  ])
  renderWithProviders(<UsersPage />)
  const user = userEvent.setup()
  const row = async (name: string) => (await screen.findByText(name)).closest("tr") as HTMLElement
  expect(within(await row("mia")).getByText("Operator on web, db")).toBeTruthy()
  expect(within(await row("mia")).getByText("Password and code, OIDC")).toBeTruthy()
  expect(within(await row("sam")).getByText("Viewer on all hosts")).toBeTruthy()
  expect(within(await row("noor")).getByText("Invited")).toBeTruthy()
  expect(within(await row("noor")).getByText("None yet")).toBeTruthy()
  expect(within(await row("ola")).getByText("Disabled")).toBeTruthy()
  expect(within(await row("admin")).getByText("All hosts")).toBeTruthy()

  const menu = async (name: string) => {
    await user.click(screen.getByRole("button", { name: `Actions for ${name}` }))
    const items = (await screen.findAllByRole("menuitem")).map((i) => i.textContent)
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("menu")).toBeNull())
    return items
  }
  const common = ["Change role", "Edit access", "Edit sign-in links"]
  expect(await menu("noor")).toEqual([...common, "New enrollment link", "Cancel invite"])
  expect(await menu("mia")).toEqual([...common, "Disable"])
  expect(await menu("ola")).toEqual([...common, "Enable"])
})

test("New enrollment link renews the invite and shows the new link once", async () => {
  const noor = dirUser({ username: "noor", state: "pending", signIn: [] })
  const calls = stub(() => [admin, noor], {
    [route("core.user.renew-invite", "plan")]: { body: makePlan("core.user.renew-invite", { confirmation: null }) },
    [route("core.user.renew-invite", "apply")]: { body: makeChange("core.user.renew-invite", { user: noor, token: "tok-new", expiresAt: "2026-11-27T09:40:00Z" }) },
  })
  renderWithProviders(<UsersPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Actions for noor" }))
  await user.click(await screen.findByRole("menuitem", { name: "New enrollment link" }))
  const change = await screen.findByRole("dialog", { name: "New enrollment link for noor" })
  await user.click(await within(change).findByRole("button", { name: "Apply" }))
  const link = await screen.findByRole("dialog", { name: "Enrollment link for noor" })
  expect((within(link).getByLabelText("Enrollment link") as HTMLInputElement).value).toBe(`${window.location.origin}/enroll#tok-new`)
  expect(calls.find((c) => c.path.endsWith("renew-invite/v1/plan"))?.body).toEqual({ input: { username: "noor" } })
})

test("Edit access plans core.user.set-grants with the edited grants", async () => {
  const mia = dirUser({ username: "mia", grants: [{ role: "viewer", target: "g2" }] })
  const calls = stub(() => [admin, mia], {
    [route("core.user.set-grants", "plan")]: { body: makePlan("core.user.set-grants", { confirmation: null }) },
  })
  renderWithProviders(<UsersPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Actions for mia" }))
  await user.click(await screen.findByRole("menuitem", { name: "Edit access" }))
  const form = await screen.findByRole("dialog", { name: "Edit the access of mia" })
  await user.click(await within(form).findByRole("checkbox", { name: "All hosts" }))
  await user.click(within(form).getByRole("checkbox", { name: "db" }))
  await user.click(within(form).getByRole("button", { name: "Review change" }))
  await screen.findByRole("dialog", { name: "Edit the access of mia" })
  await waitFor(() => expect(calls.find((c) => c.path.endsWith("set-grants/v1/plan"))?.body).toEqual({ input: { username: "mia", grants: [{ role: "viewer", target: "*" }] } }))
})

test("UX-6: a failed load says so and retries", async () => {
  let fail = true
  stubFetch({
    [route("core.user.directory", "read")]: () =>
      fail ? { status: 500, body: { type: "about:blank", title: "x", status: 500, detail: "boom", code: "internal" } } : { body: { output: { users: [admin] } } },
    [route("core.group.list", "read")]: { body: { output: { groups } } },
  })
  renderWithProviders(<UsersPage />)
  expect(await screen.findByText(/^Couldn't load users/)).toBeTruthy()
  fail = false
  await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
  expect(await screen.findByText("admin")).toBeTruthy()
})
