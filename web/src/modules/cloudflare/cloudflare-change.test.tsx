import { useState } from "react"
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Change, Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { CloudflareChange } from "./cloudflare-change"
import { cloudflareKey } from "./types"

const CC_PLAN = "POST /api/v1/actions/cloudflare.hostname.add/v1/plan"
const CC_APPLY = "POST /api/v1/actions/cloudflare.hostname.add/v1/apply"
const CC_INPUT = { tunnel: "darkpanel-01JD0000000000000000000001", hostname: "app.example.com", service: "http://172.18.0.2:8080" }

function ccPlan(over: Partial<Plan> = {}): Plan {
  return {
    planId: "p-hn", hostId: null, action: "cloudflare.hostname.add", version: 1, inputHash: "h",
    diff: [{ op: "+", text: "route app.example.com to http://172.18.0.2:8080" }], guards: [],
    expiresAt: "2026-10-30T13:00:00Z", confirmation: null, ...over,
  }
}

function ccChange(over: Partial<Change> = {}): Change {
  return {
    id: "c-hn", hostId: null, action: "cloudflare.hostname.add", version: 1, class: "compensable", state: "completed", actor: "user:u1",
    createdAt: "2026-10-30T12:00:00Z", updatedAt: "2026-10-30T12:00:00Z", revertDeadline: null, output: null, error: null,
    compensatedBy: null, parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null, ...over,
  }
}

function CcHost({ onClosed }: { onClosed: () => void }) {
  const [input, setInput] = useState<Record<string, unknown> | null>(null)
  return (
    <>
      <button onClick={() => setInput(CC_INPUT)}>Open</button>
      <CloudflareChange
        action="cloudflare.hostname.add"
        title="Add hostname"
        input={input}
        onClose={() => {
          setInput(null)
          onClosed()
        }}
      />
    </>
  )
}

function ccRender(routes: Record<string, StubHandler>) {
  const calls = stubFetch(routes)
  const closed = { count: 0 }
  const result = renderWithProviders(<CcHost onClosed={() => (closed.count += 1)} />)
  result.client.setQueryData(cloudflareKey("hostnames"), { hostnames: [] })
  result.client.setQueryData(["tailnet", "account"], { connected: false })
  return { calls, closed, ...result }
}

test("a null input keeps the dialog closed and plans nothing", () => {
  const { calls } = ccRender({ [CC_PLAN]: { body: ccPlan() } })
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toHaveLength(0)
})

test("plans on the controller, shows the diff and applies the plan", async () => {
  const { calls } = ccRender({ [CC_PLAN]: { body: ccPlan() }, [CC_APPLY]: { body: ccChange() } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Add hostname" })
  expect(within(dialog).getByText("cloudflare.hostname.add/v1 on the controller").textContent).toBe("cloudflare.hostname.add/v1 on the controller")
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ route app.example.com to http://172.18.0.2:8080")
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  expect((await within(dialog).findByText("completed")).textContent).toBe("completed")
  expect(calls.filter((c) => c.method === "POST").map((c) => [c.path, c.body])).toEqual([
    ["/api/v1/actions/cloudflare.hostname.add/v1/plan", { input: CC_INPUT }],
    ["/api/v1/actions/cloudflare.hostname.add/v1/apply", { planId: "p-hn" }],
  ])
  expect(calls.some((c) => c.path.includes("/availability"))).toBe(false)
  expect(within(dialog).queryByLabelText("Revert after (seconds)")).toBeNull()
})

test("closing the dialog calls onClose once and refreshes the Cloudflare queries only", async () => {
  const { closed, client } = ccRender({ [CC_PLAN]: { body: ccPlan() }, [CC_APPLY]: { body: ccChange() } })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Add hostname" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  // The dialog has a close icon and a footer button, both named Close.
  const closers = await within(dialog).findAllByRole("button", { name: "Close" })
  await user.click(closers[0] as HTMLElement)
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(closed.count).toBe(1)
  expect(client.getQueryState(cloudflareKey("hostnames"))?.isInvalidated).toBe(true)
  expect(client.getQueryState(["tailnet", "account"])?.isInvalidated).toBe(false)
})

test("escape closes a dialog whose plan was refused and still refreshes", async () => {
  const refusal = { status: 409, body: { type: "about:blank", title: "Conflict", status: 409, detail: "the tunnel's configuration changed outside Darkpanel", code: "cloud.conflict" } }
  const { closed, client } = ccRender({ [CC_PLAN]: refusal })
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Add hostname" })
  expect((await within(dialog).findByRole("alert")).textContent).toBe("the tunnel's configuration changed outside Darkpanel")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(closed.count).toBe(1)
  expect(client.getQueryState(cloudflareKey("hostnames"))?.isInvalidated).toBe(true)
})

test("the open dialog has no accessibility violations", async () => {
  ccRender({ [CC_PLAN]: { body: ccPlan() } })
  await userEvent.setup().click(screen.getByRole("button", { name: "Open" }))
  const dialog = await screen.findByRole("dialog", { name: "Add hostname" })
  await within(dialog).findByLabelText("Planned changes")
  expect(await axe(dialog)).toHaveNoViolations()
})
