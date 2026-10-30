import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Change, Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { tnPolicy } from "./fixtures"
import { PolicyPage } from "./policy-page"

const GET = "POST /api/v1/actions/tailnet.policy.get/v1/read"
const PLAN = "POST /api/v1/actions/tailnet.policy.set/v1/plan"
const APPLY = "POST /api/v1/actions/tailnet.policy.set/v1/apply"

const ppText = '{\n  // Everyone reaches everything.\n  "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}],\n}\n'
const ppEdited = ppText.replace('"*:*"', '"tag:web:443"')

const ppPlan: Plan = {
  planId: "p1", hostId: null, action: "tailnet.policy.set", version: 1, inputHash: "h",
  diff: [
    { op: " ", text: '  "acls": [{"action": "accept", "src": ["*"],' },
    { op: "-", text: '"dst": ["*:*"]}],' },
    { op: "+", text: '"dst": ["tag:web:443"]}],' },
  ],
  guards: [], expiresAt: "2026-10-30T09:00:00Z", confirmation: null,
}

const ppChange: Change = {
  id: "c1", hostId: null, action: "tailnet.policy.set", version: 1, class: "compensable", state: "completed", actor: "user:u1",
  createdAt: "2026-10-30T08:00:00Z", updatedAt: "2026-10-30T08:00:00Z", revertDeadline: null, output: { etag: "etag-2" }, error: null,
  compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function ppRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({ [GET]: { body: { output: tnPolicy({ text: ppText, etag: "etag-1" }) } }, [PLAN]: { body: ppPlan }, ...routes })
  const result = renderWithProviders(<PolicyPage />)
  return { calls, ...result }
}

async function ppEditor(): Promise<HTMLTextAreaElement> {
  return (await screen.findByLabelText("Policy file (HuJSON)")) as HTMLTextAreaElement
}

// ppEdit replaces the whole text; paste keeps the braces literal, which user.type would read as key names.
async function ppEdit(user: ReturnType<typeof userEvent.setup>, box: HTMLElement, text: string) {
  await user.clear(box)
  await user.click(box)
  await user.paste(text)
}

test("shows the policy file as the provider stores it, with its version", async () => {
  ppRender()
  const box = await ppEditor()
  expect(box.value).toBe(ppText)
  expect(screen.getByText("Version etag-1").textContent).toBe("Version etag-1")
  expect(screen.getByRole("button", { name: "Review changes" }).hasAttribute("disabled")).toBe(true)
  expect(screen.getByRole("button", { name: "Discard edits" }).hasAttribute("disabled")).toBe(true)
})

test("plans the edited text and shows the line diff", async () => {
  const { calls } = ppRender()
  const user = userEvent.setup()
  await ppEdit(user, await ppEditor(), ppEdited)
  await user.click(screen.getByRole("button", { name: "Review changes" }))
  const dialog = await screen.findByRole("dialog", { name: "Apply the policy file" })
  const diff = await within(dialog).findByLabelText("Planned changes")
  expect(diff.textContent).toContain('- "dst": ["*:*"]}],')
  expect(diff.textContent).toContain('+ "dst": ["tag:web:443"]}],')
  const plan = calls.find((c) => c.path === "/api/v1/actions/tailnet.policy.set/v1/plan")
  expect(plan?.body).toEqual({ input: { text: ppEdited } })
})

test("a policy the provider rejects is reported and cannot be applied", async () => {
  ppRender({
    [PLAN]: { status: 422, body: { type: "about:blank", title: "Invalid", status: 422, detail: "line 3: unknown field \"acl\"", code: "tailnet.policy_invalid" } },
  })
  const user = userEvent.setup()
  await ppEdit(user, await ppEditor(), ppEdited)
  await user.click(screen.getByRole("button", { name: "Review changes" }))
  const dialog = await screen.findByRole("dialog", { name: "Apply the policy file" })
  expect((await within(dialog).findByRole("alert")).textContent).toBe('line 3: unknown field "acl"')
  expect(within(dialog).getByRole("button", { name: "Apply" }).hasAttribute("disabled")).toBe(true)
})

test("applying reads the policy again and shows the stored text", async () => {
  let stored = tnPolicy({ text: ppText, etag: "etag-1" })
  const { calls } = ppRender({
    [GET]: () => ({ body: { output: stored } }),
    [APPLY]: () => {
      stored = tnPolicy({ text: ppEdited, etag: "etag-2" })
      return { body: ppChange }
    },
  })
  const user = userEvent.setup()
  await ppEdit(user, await ppEditor(), ppEdited)
  await user.click(screen.getByRole("button", { name: "Review changes" }))
  const dialog = await screen.findByRole("dialog", { name: "Apply the policy file" })
  await within(dialog).findByLabelText("Planned changes")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  // The dialog's corner button has the same name; the footer's is the last.
  await user.click(within(dialog).getAllByRole("button", { name: "Close" }).slice(-1)[0] as HTMLElement)
  await waitFor(() => expect(screen.getByText("Version etag-2").textContent).toBe("Version etag-2"))
  expect(((await ppEditor()).value)).toBe(ppEdited)
  expect(screen.getByRole("button", { name: "Review changes" }).hasAttribute("disabled")).toBe(true)
  expect(calls.filter((c) => c.path.endsWith("tailnet.policy.get/v1/read"))).toHaveLength(2)
})

test("discarding the edits restores the stored text", async () => {
  ppRender()
  const user = userEvent.setup()
  const box = await ppEditor()
  await ppEdit(user, box, ppEdited)
  expect(box.value).toBe(ppEdited)
  await user.click(screen.getByRole("button", { name: "Discard edits" }))
  expect(box.value).toBe(ppText)
  expect(screen.getByRole("button", { name: "Review changes" }).hasAttribute("disabled")).toBe(true)
})

test("a policy that changes on the provider replaces an edit made on the old version", async () => {
  let stored = tnPolicy({ text: ppText, etag: "etag-1" })
  const { client } = ppRender({ [GET]: () => ({ body: { output: stored } }) })
  const user = userEvent.setup()
  await ppEdit(user, await ppEditor(), ppEdited)
  stored = tnPolicy({ text: '{"acls": []}\n', etag: "etag-9" })
  await client.invalidateQueries({ queryKey: ["tailnet"] })
  await waitFor(() => expect(screen.getByText("Version etag-9").textContent).toBe("Version etag-9"))
  expect((await ppEditor()).value).toBe('{"acls": []}\n')
})

test("a tailnet with no account shows the reason instead of an editor", async () => {
  ppRender({ [GET]: { status: 409, body: { type: "about:blank", title: "Not connected", status: 409, detail: "no tailnet account is connected", code: "cloud.not_connected" } } })
  expect((await screen.findByRole("alert")).textContent).toBe("no tailnet account is connected")
  expect(screen.queryByLabelText("Policy file (HuJSON)")).toBeNull()
})

test("has no accessibility violations", async () => {
  const { container } = ppRender()
  await ppEditor()
  expect(await axe(container)).toHaveNoViolations()
})
