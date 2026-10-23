import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Change, NewToken, Plan } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { CreateTokenDialog } from "./create-token-dialog"

const PLAN = "POST /api/v1/actions/core.token.create/v1/plan"
const APPLY = "POST /api/v1/actions/core.token.create/v1/apply"

const plan: Plan = {
  planId: "p1", hostId: null, action: "core.token.create", version: 1, inputHash: "h",
  diff: [{ op: "+", text: "token ci" }], guards: [], expiresAt: "2026-10-19T09:00:00Z", confirmation: null,
}

const created: NewToken = {
  id: "tok1", name: "ci", scope: "operate", createdAt: "2026-10-19T08:00:00Z", expiresAt: "2026-11-18T08:00:00Z",
  lastUsedAt: null, revokedAt: null, token: "dpt_example_secret",
}

function change(state: string, output: NewToken | null, error: Change["error"] = null): Change<NewToken> {
  return {
    id: "c1", hostId: null, action: "core.token.create", version: 1, class: "compensable", state, actor: "user:u1",
    createdAt: "2026-10-19T08:00:00Z", updatedAt: "2026-10-19T08:00:00Z", revertDeadline: null, output, error, compensatedBy: null,
    parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
  }
}

async function fillAndCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Create token" }))
  const dialog = screen.getByRole("dialog")
  await user.type(within(dialog).getByLabelText("Name"), "ci")
  await user.click(within(dialog).getByRole("radio", { name: "Operate" }))
  const days = within(dialog).getByLabelText("Expires after (days)")
  await user.clear(days)
  await user.type(days, "30")
  await user.click(within(dialog).getByRole("button", { name: "Create" }))
  return dialog
}

test("creates a token and shows it once with a copy button", async () => {
  const calls = stubFetch({ [PLAN]: { body: plan }, [APPLY]: { body: change("completed", created) } })
  renderWithProviders(<CreateTokenDialog />)
  const user = userEvent.setup()
  const dialog = await fillAndCreate(user)

  expect((await within(dialog).findByText("Copy the token now. It will not be shown again.")).textContent).toBe(
    "Copy the token now. It will not be shown again.",
  )
  expect(within(dialog).getByDisplayValue("dpt_example_secret")).toBeTruthy()
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { name: "ci", scope: "operate", expiresInDays: 30 } })
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })

  await user.click(within(dialog).getByRole("button", { name: "Copy" }))
  expect(await navigator.clipboard.readText()).toBe("dpt_example_secret")

  await user.click(within(dialog).getByRole("button", { name: "Done" }))
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await user.click(screen.getByRole("button", { name: "Create token" }))
  expect(screen.queryByDisplayValue("dpt_example_secret")).toBeNull()
  expect(within(screen.getByRole("dialog")).getByLabelText("Name")).toBeTruthy()
})

test("the dialog stays open and submits once while a creation is in flight", async () => {
  let release: () => void = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  const calls = stubFetch({
    [PLAN]: { body: plan },
    [APPLY]: async () => {
      await gate
      return { body: change("completed", created) }
    },
  })
  renderWithProviders(<CreateTokenDialog />)
  const user = userEvent.setup()
  const dialog = await fillAndCreate(user)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))

  await user.keyboard("{Escape}")
  expect(screen.getByRole("dialog")).toBe(dialog)
  await user.click(within(dialog).getByRole("button", { name: /Create/ }))
  expect(calls.filter((c) => c.path.endsWith("/plan"))).toHaveLength(1)

  release()
  expect(await within(dialog).findByDisplayValue("dpt_example_secret")).toBeTruthy()
  expect(calls.filter((c) => c.path.endsWith("/apply"))).toHaveLength(1)
})

test("shows the change's error when the creation failed", async () => {
  stubFetch({ [PLAN]: { body: plan }, [APPLY]: { body: change("failed", null, { code: "internal", message: "the token store is busy" }) } })
  renderWithProviders(<CreateTokenDialog />)
  const dialog = await fillAndCreate(userEvent.setup())
  expect((await within(dialog).findByText("the token store is busy")).textContent).toBe("the token store is busy")
  expect(within(dialog).queryByText("Copy the token now. It will not be shown again.")).toBeNull()
})

test("shows a refusal from the plan", async () => {
  stubFetch({
    [PLAN]: { status: 403, body: { type: "about:blank", title: "Forbidden", status: 403, detail: "this request needs the admin scope", code: "auth.forbidden" } },
  })
  renderWithProviders(<CreateTokenDialog />)
  const dialog = await fillAndCreate(userEvent.setup())
  expect((await within(dialog).findByText("this request needs the admin scope")).textContent).toBe("this request needs the admin scope")
})

test("lists the field errors of an invalid input", async () => {
  stubFetch({
    [PLAN]: {
      status: 422,
      body: {
        type: "about:blank", title: "Invalid input", status: 422, detail: "invalid request", code: "request.invalid_input",
        errors: [
          { path: "/name", message: "must not contain control characters" },
          { path: "/expiresInDays", message: "must be at most 365" },
        ],
      },
    },
  })
  renderWithProviders(<CreateTokenDialog />)
  const dialog = await fillAndCreate(userEvent.setup())
  expect((await within(dialog).findByText("invalid request")).textContent).toBe("invalid request")
  expect(within(dialog).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
    "/name: must not contain control characters",
    "/expiresInDays: must be at most 365",
  ])
})

test("asks for a name when the name is blank and sends nothing", async () => {
  const calls = stubFetch({ [PLAN]: { body: plan } })
  renderWithProviders(<CreateTokenDialog />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Create token" }))
  const dialog = screen.getByRole("dialog")
  await user.type(within(dialog).getByLabelText("Name"), "   ")
  await user.click(within(dialog).getByRole("button", { name: "Create" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("Enter a name for the token.")
  expect(calls).toHaveLength(0)
})
