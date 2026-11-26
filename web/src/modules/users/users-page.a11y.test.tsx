import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, test, vi } from "vitest"
import { accountsDefault, accountsWorst, keysDeploy, sshdState } from "@/modules/m11-fixtures"
import { expectNoAxeViolations } from "@/test/a11y"
import { makePlan, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { UsersPage } from "./users-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const LIST = `${BASE}/users.user.list/v1/read`
const THEMES = ["light", "dark"] as const
const OK = { body: { output: { accounts: accountsDefault } } }
const NO_WAY_IN = "After the change no account could sign in over SSH. Considered: root (no password login), deploy (no key)."

const STATES: [string, StubHandler, () => Promise<void>][] = [
  ["loading", () => new Promise(() => {}), async () => {
    await screen.findByRole("status", { name: "Loading the users" })
  }],
  ["error", { status: 500, body: { code: "internal", detail: "boom", status: 500 } }, async () => {
    await screen.findByText("Couldn't load users")
  }],
  ["default", OK, async () => {
    await screen.findByRole("button", { name: "deploy" })
    await screen.findByText("Keys only")
  }],
  ["worst case", { body: { output: { accounts: accountsWorst.slice(0, 40) } } }, async () => {
    await screen.findByRole("button", { name: accountsWorst[4]?.name ?? "" })
  }],
  ["add user dialog with a name error", OK, async () => {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add user" }))
    await user.type(screen.getByLabelText("Name"), "Deploy")
    await screen.findByText("Use lowercase letters, digits, - and _, starting with a letter or _")
  }],
  ["user sheet with a key error", OK, async () => {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "deploy" }))
    await screen.findByText("deploy@laptop")
    await user.type(screen.getByLabelText("Add a public key"), "hello")
    await screen.findByText("Not a public key line")
  }],
  ["hardening form", OK, async () => {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Edit hardening" }))
    await screen.findByText("Starts from the values sshd runs with now.")
  }],
  ["hardening with a guard error", OK, async () => {
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Edit hardening" }))
    await user.click(screen.getByRole("switch", { name: "Password login" }))
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await screen.findByText(NO_WAY_IN)
  }],
]

afterEach(() => {
  document.documentElement.classList.remove("dark")
  vi.unstubAllGlobals()
})

describe("no axe violations in any state", () => {
  for (const theme of THEMES) {
    test.each(STATES)(`%s (${theme})`, async (_name, list, reach) => {
      document.documentElement.classList.toggle("dark", theme === "dark")
      stubFetch({
        [LIST]: list,
        [`${BASE}/users.key.list/v1/read`]: { body: { output: keysDeploy } },
        [`${BASE}/users.sshd.get/v1/read`]: { body: { output: sshdState } },
        [`${BASE}/users.sshd.apply/v1/plan`]: {
          body: makePlan("users.sshd.apply", { hostId: "h1", guards: [{ id: "users.no_way_in", ok: false, overridden: false, overridable: true, message: NO_WAY_IN }] }),
        },
        "GET /api/v1/hosts/h1/modules/users/availability": { body: { module: "users", available: true, reason: null, actions: [] } },
      })
      const { container } = renderWithProviders(<UsersPage hostId="h1" />, { hostScope: "admin" })
      await reach()
      await expectNoAxeViolations(container)
      for (const dialog of document.querySelectorAll("[role=dialog]")) await expectNoAxeViolations(dialog)
    }, 20_000)
  }
})
