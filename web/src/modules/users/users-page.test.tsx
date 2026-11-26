import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { afterEach, describe, expect, test, vi } from "vitest"
import type { Scope } from "@/api/types"
import { accountsDefault, accountsWorst, keysDeploy, sshdState } from "@/modules/m11-fixtures"
import { makeChange, makePlan, renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { keyError, keyType, NOT_A_KEY, RSA_TOO_SHORT } from "./keys"
import { UsersPage } from "./users-page"

const BASE = "POST /api/v1/hosts/h1/actions"
const LIST = `${BASE}/users.user.list/v1/read`
const KEYS = `${BASE}/users.key.list/v1/read`
const SSHD = `${BASE}/users.sshd.get/v1/read`
const AVAILABILITY = "GET /api/v1/hosts/h1/modules/users/availability"

// sshLine is a public key line whose blob holds the given SSH wire fields.
function sshLine(type: string, ...fields: Uint8Array[]): string {
  const parts = [new TextEncoder().encode(type), ...fields]
  const size = parts.reduce((n, p) => n + 4 + p.length, 0)
  const blob = new Uint8Array(size)
  let at = 0
  for (const p of parts) {
    new DataView(blob.buffer).setUint32(at, p.length)
    blob.set(p, at + 4)
    at += 4 + p.length
  }
  return `${type} ${btoa(String.fromCharCode(...blob))} test@laptop`
}

function rsaLine(bits: number): string {
  const n = new Uint8Array(bits / 8 + 1)
  n[1] = 0x80
  return sshLine("ssh-rsa", Uint8Array.of(1, 0, 1), n)
}

const ED25519 = sshLine("ssh-ed25519", new Uint8Array(32).fill(7))

function render(extra: Record<string, StubHandler> = {}, hostScope: Scope = "admin") {
  const calls = stubFetch({
    [LIST]: { body: { output: { accounts: accountsDefault } } },
    [KEYS]: { body: { output: keysDeploy } },
    [SSHD]: { body: { output: sshdState } },
    [AVAILABILITY]: { body: { module: "users", available: true, reason: null, actions: [] } },
    ...extra,
  })
  renderWithProviders(<UsersPage hostId="h1" />, { hostScope })
  return calls
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("key checks", () => {
  test("ed25519, ecdsa and 3072-bit RSA pass; short RSA, junk and options fail", () => {
    expect(keyError(ED25519)).toBeNull()
    expect(keyError(sshLine("ecdsa-sha2-nistp256", new TextEncoder().encode("nistp256"), new Uint8Array(65)))).toBeNull()
    expect(keyError(rsaLine(3072))).toBeNull()
    expect(keyError(rsaLine(2048))).toBe(RSA_TOO_SHORT)
    expect(keyError("hello")).toBe(NOT_A_KEY)
    expect(keyError(`from="10.0.0.0/8" ${ED25519}`)).toBe(NOT_A_KEY)
    expect(keyError("ssh-ed25519 !!!notbase64")).toBe(NOT_A_KEY)
    expect(keyError(sshLine("ssh-dss", new Uint8Array(20)))).toBe(NOT_A_KEY)
    expect(keyError(ED25519.replace("ssh-ed25519 ", "ssh-rsa "))).toBe(NOT_A_KEY)
  })

  test("a key's type reads as its name and size", () => {
    expect(keysDeploy.keys.map(keyType)).toEqual(["ED25519 256", "RSA 4096"])
    expect(keyType({ type: "sk-ssh-ed25519@openssh.com", bits: 256, fingerprint: "", comment: "", options: "" })).toBe("SK-ED25519 256")
  })
})

describe("forms validate inline", () => {
  test("the table shows sudo and locked badges, the password state and the key count", async () => {
    render()
    const row = (await screen.findByRole("button", { name: "alice" })).closest("tr") as HTMLElement
    expect(within(row).getByText("Locked")).toBeTruthy()
    expect(within(row).queryByText("Sudo")).toBeNull()
    expect(within(row).getByText("Set")).toBeTruthy()
    expect(within(row).getByText("0")).toBeTruthy()
  })

  test("name Alice shows the name rule", async () => {
    render()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add user" }))
    await user.type(screen.getByLabelText("Name"), "Alice")
    expect(screen.getByText("Use lowercase letters, digits, - and _, starting with a letter or _")).toBeTruthy()
    expect(screen.getByLabelText("Name").getAttribute("aria-invalid")).toBe("true")
    await user.clear(screen.getByLabelText("Name"))
    await user.type(screen.getByLabelText("Name"), "deploy")
    expect(screen.getByText("An account named deploy exists")).toBeTruthy()
  })

  test("a 2048-bit RSA key shows RSA keys need at least 3072 bits; hello shows Not a public key line", async () => {
    render()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "deploy" }))
    const field = await screen.findByLabelText("Add a public key")
    await user.click(field)
    await user.paste(rsaLine(2048))
    expect(screen.getByText(RSA_TOO_SHORT)).toBeTruthy()
    expect((screen.getByRole("button", { name: "Add key" }) as HTMLButtonElement).disabled).toBe(true)
    await user.clear(field)
    await user.type(field, "hello")
    expect(screen.getByText(NOT_A_KEY)).toBeTruthy()
  })

  test("the user sheet lists the keys with type, fingerprint and comment", async () => {
    render()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "deploy" }))
    const table = await screen.findByRole("table", { name: "Authorized keys" })
    expect(within(table).getByText("ED25519 256")).toBeTruthy()
    expect(within(table).getByText(keysDeploy.keys[0]?.fingerprint ?? "")).toBeTruthy()
    expect(within(table).getByText("deploy@laptop")).toBeTruthy()
  })

  test("the password fields clear after apply and never show the value again", async () => {
    const plans: unknown[] = []
    render({
      [`${BASE}/users.password.set/v1/plan`]: (call) => {
        plans.push(call.body)
        return { body: makePlan("users.password.set", { hostId: "h1" }) }
      },
      [`${BASE}/users.password.set/v1/apply`]: { body: makeChange("users.password.set", { name: "alice" }, { class: "compensable" }) },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Actions for alice" }))
    await user.click(await screen.findByRole("menuitem", { name: "Set password" }))
    await user.type(screen.getByLabelText("Password"), "correct horse")
    await user.type(screen.getByLabelText("Confirm the password"), "correct horse")
    await user.click(screen.getByRole("button", { name: "Review change" }))
    await user.click(await screen.findByRole("button", { name: "Apply" }))
    await screen.findByTestId("change-done")
    expect(plans).toEqual([{ input: { name: "alice", password: "correct horse" } }])
    expect(document.body.textContent).not.toContain("correct horse")
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    await user.click(screen.getByRole("button", { name: "Actions for alice" }))
    await user.click(await screen.findByRole("menuitem", { name: "Set password" }))
    expect((screen.getByLabelText("Password") as HTMLInputElement).value).toBe("")
    expect((screen.getByLabelText("Confirm the password") as HTMLInputElement).value).toBe("")
  })

  test("mismatched passwords show The passwords do not match", async () => {
    render()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add user" }))
    await user.type(screen.getByLabelText("Password"), "correct horse")
    await user.type(screen.getByLabelText("Confirm the password"), "correct hors")
    expect(screen.getByText("The passwords do not match")).toBeTruthy()
  })

  test("delete asks the user name", async () => {
    render({
      [`${BASE}/users.user.delete/v1/plan`]: { body: makePlan("users.user.delete", { hostId: "h1", confirmation: "alice" }) },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Actions for alice" }))
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }))
    const dialog = await screen.findByRole("dialog")
    expect(await within(dialog).findByLabelText("Type alice to confirm")).toBeTruthy()
  })

  test("the root row has no Lock or Delete", async () => {
    render()
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Actions for root" }))
    const items = (await screen.findAllByRole("menuitem")).map((i) => i.textContent)
    expect(items).toEqual(["Keys", "Set password"])
    await user.keyboard("{Escape}")
    await user.click(screen.getByRole("button", { name: "Actions for backup" }))
    expect((await screen.findAllByRole("menuitem")).map((i) => i.textContent)).toEqual(["Keys", "Set password", "Add to sudo", "Lock", "Delete"])
  })

  test("Add user with sudo opens the step-up dialog outside the window", async () => {
    let planned = 0
    const calls = render({
      [`${BASE}/users.user.create/v1/plan`]: () =>
        ++planned === 1
          ? { status: 403, body: { status: 403, code: "auth.step_up_required", detail: "enter an authenticator code to continue" } }
          : { body: makePlan("users.user.create", { hostId: "h1" }) },
      "POST /api/v1/auth/step-up": { status: 204 },
    })
    const user = userEvent.setup()
    await user.click(await screen.findByRole("button", { name: "Add user" }))
    await user.type(screen.getByLabelText("Name"), "carol")
    await user.click(screen.getByLabelText("Can use sudo"))
    await user.click(screen.getByRole("button", { name: "Review change" }))
    expect(await screen.findByText("Enter your authenticator code")).toBeTruthy()
    await user.type(screen.getByLabelText("Authenticator code"), "123456")
    await user.click(screen.getByRole("button", { name: "Verify" }))
    await screen.findByRole("button", { name: "Apply" })
    const plan = calls.filter((c) => c.path.includes("users.user.create"))
    expect(plan.map((c) => c.body)).toEqual([{ input: { name: "carol", sudo: true } }, { input: { name: "carol", sudo: true } }])
  })

  test("a viewer sees the table with no Add user and no menus", async () => {
    render({}, "read")
    await screen.findByRole("button", { name: "deploy" })
    expect(screen.queryByRole("button", { name: "Add user" })).toBeNull()
    expect(screen.queryByRole("button", { name: /^Actions for/ })).toBeNull()
  })

  test("loading, error with Retry and 200 accounts with 32-character names", async () => {
    let calls = 0
    render({ [LIST]: () => (++calls === 1 ? { status: 500, body: { code: "internal", detail: "boom", status: 500 } } : { body: { output: { accounts: accountsWorst } } }) })
    expect(screen.getByRole("status", { name: "Loading the users" })).toBeTruthy()
    expect(await screen.findByText("Couldn't load users")).toBeTruthy()
    await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
    const name = accountsWorst[4]?.name ?? ""
    expect(name).toHaveLength(32)
    const cell = await screen.findByRole("button", { name })
    expect(cell.getAttribute("title")).toBe(name)
    expect(within(screen.getByRole("table", { name: "Login accounts" })).getAllByRole("row")).toHaveLength(201)
  }, 20_000)
})
