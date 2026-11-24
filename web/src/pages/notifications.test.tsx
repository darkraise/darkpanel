import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { HostGroup, NotifyChannel } from "@/api/types"
import { makeChange, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { NotificationsPage } from "./notifications"

const route = (action: string, op: string) => `POST /api/v1/actions/${action}/v1/${op}`

function channel(over: Partial<NotifyChannel>): NotifyChannel {
  return { id: "c1", kind: "ntfy", name: "ops-phone", minSeverity: "warning", kinds: [], groupIds: [], enabled: true, createdAt: "2026-11-20T08:00:00Z", lastDelivery: null, ...over }
}

const groups: HostGroup[] = [
  { id: "g1", name: "web", hostIds: [], hostCount: 0, grantCount: 0, createdAt: "2026-11-20T08:00:00Z" },
  { id: "g2", name: "db", hostIds: [], hostCount: 0, grantCount: 0, createdAt: "2026-11-20T08:00:00Z" },
]

const phone = channel({ id: "c1", name: "ops-phone", ntfy: { server: "https://ntfy.sh", topic: "ops-…", tokenSet: false }, lastDelivery: { state: "sent", at: "2026-11-24T09:12:00Z" } })
const chat = channel({
  id: "c2", kind: "telegram", name: "team-chat", minSeverity: "critical", kinds: ["alert.firing", "alert.resolved"], groupIds: ["g1"],
  telegram: { chatId: "-100123" }, lastDelivery: { state: "failed", at: "2026-11-24T09:00:00Z", error: "403" },
})
const oncall = channel({
  id: "c3", kind: "email", name: "oncall", minSeverity: "critical", groupIds: ["g1", "g2"],
  email: { host: "smtp.example.com", port: 587, security: "starttls", username: "dp", passwordSet: true, from: "dp@example.com", recipients: ["a@example.com"] },
})

function stub(channels: NotifyChannel[], extra: Parameters<typeof stubFetch>[0] = {}) {
  return stubFetch({
    [route("notify.channel.list", "read")]: { body: { output: { channels } } },
    [route("core.group.list", "read")]: { body: { output: { groups } } },
    ...extra,
  })
}

const card = (name: string) => screen.findByRole("region", { name })

test("UI-4 each card shows its kind, name, routing summary and last result as text", async () => {
  stub([phone, chat, oncall])
  renderWithProviders(<NotificationsPage />)
  const p = await card("ops-phone")
  expect(within(p).getByText("ntfy")).toBeTruthy()
  expect(within(p).getByText("Warning and above, all events, all hosts")).toBeTruthy()
  expect(within(p).getByText(/^Last delivery: Delivered /)).toBeTruthy()
  const c = await card("team-chat")
  expect(within(c).getByText("Telegram")).toBeTruthy()
  expect(within(c).getByText("Critical, alerts only, group web")).toBeTruthy()
  expect(within(c).getByText("Last delivery: Failed: 403")).toBeTruthy()
  const o = await card("oncall")
  expect(within(o).getByText("Critical, all events, groups web, db")).toBeTruthy()
  expect(within(o).getByText("Last delivery: No deliveries yet")).toBeTruthy()
})

test("UX-4 add an email channel: inline checks, then plan and apply", async () => {
  const calls = stub([], {
    [route("notify.channel.create", "plan")]: { body: makePlan("notify.channel.create", { confirmation: null }) },
    [route("notify.channel.create", "apply")]: { body: makeChange("notify.channel.create", oncall) },
  })
  renderWithProviders(<NotificationsPage />)
  const user = userEvent.setup()
  expect(await screen.findByText("No channels yet")).toBeTruthy()
  await user.click(screen.getByRole("button", { name: "Add channel" }))
  const picker = await screen.findByRole("dialog", { name: "Add channel" })
  await user.click(within(picker).getByRole("button", { name: "Email" }))
  const form = await screen.findByRole("dialog", { name: "Add an email channel" })
  await user.type(within(form).getByLabelText("Name"), "oncall")
  await user.clear(within(form).getByLabelText("Port"))
  await user.type(within(form).getByLabelText("Port"), "70000")
  await user.type(within(form).getByLabelText("Recipients"), Array.from({ length: 11 }, (_, i) => `u${i}@example.com`).join(", "))
  await user.click(within(form).getByRole("button", { name: "Review channel" }))
  expect(within(form).getByText("Port must be 1 to 65535.")).toBeTruthy()
  expect(within(form).getByText("Enter 1 to 10 addresses.")).toBeTruthy()
  expect(within(form).getByText("Enter the SMTP host.")).toBeTruthy()
  expect(within(form).getByLabelText("Port").getAttribute("aria-invalid")).toBe("true")
  expect(calls.some((c) => c.path.includes("notify.channel.create"))).toBe(false)

  await user.type(within(form).getByLabelText("SMTP host"), "smtp.example.com")
  await user.clear(within(form).getByLabelText("Port"))
  await user.type(within(form).getByLabelText("Port"), "465")
  await user.click(within(form).getByRole("radio", { name: "TLS" }))
  await user.type(within(form).getByLabelText("Password (optional)"), "pw")
  await user.type(within(form).getByLabelText("From"), "dp@example.com")
  await user.clear(within(form).getByLabelText("Recipients"))
  await user.type(within(form).getByLabelText("Recipients"), "a@example.com\nb@example.com")
  await user.click(within(form).getByRole("radio", { name: "Critical" }))
  await user.click(within(form).getByRole("checkbox", { name: "Alerts" }))
  await user.click(await within(form).findByRole("checkbox", { name: "db" }))
  await user.click(within(form).getByRole("button", { name: "Review channel" }))

  const change = await screen.findByRole("dialog", { name: "Add the channel oncall" })
  await user.click(await within(change).findByRole("button", { name: "Apply" }))
  expect(calls.find((c) => c.path.endsWith("notify.channel.create/v1/plan"))?.body).toEqual({
    input: {
      kind: "email", name: "oncall", minSeverity: "critical", kinds: ["alert.firing", "alert.resolved"], groupIds: ["g2"],
      email: { host: "smtp.example.com", port: 465, security: "tls", from: "dp@example.com", recipients: ["a@example.com", "b@example.com"], password: "pw" },
    },
  })
})

test("UX-4 the ntfy form checks the server URL and needs a topic", async () => {
  stub([])
  renderWithProviders(<NotificationsPage />)
  const user = userEvent.setup()
  await user.click(await screen.findByRole("button", { name: "Add channel" }))
  await user.click(within(await screen.findByRole("dialog", { name: "Add channel" })).getByRole("button", { name: "ntfy" }))
  const form = await screen.findByRole("dialog", { name: "Add a ntfy channel" })
  await user.type(within(form).getByLabelText("Name"), "phone")
  await user.type(within(form).getByLabelText("Server URL"), "ntfy.example.com")
  await user.click(within(form).getByRole("button", { name: "Review channel" }))
  expect(within(form).getByText("Enter a URL that starts with http:// or https://.")).toBeTruthy()
  expect(within(form).getByText("Enter the topic.")).toBeTruthy()
  await user.click(within(form).getByRole("button", { name: "Back" }))
  expect(await screen.findByRole("dialog", { name: "Add channel" })).toBeTruthy()
})

test("A38 Send test shows pending, then Delivered or Failed: <status> in the card", async () => {
  let release: (() => void) | null = null
  stub([phone, chat], {
    [route("notify.channel.test", "read")]: async (call) => {
      const id = (call.body as { input: { id: string } }).input.id
      if (id === "c1") {
        await new Promise<void>((resolve) => {
          release = resolve
        })
        return { body: { output: { delivered: true, code: "200" } } }
      }
      return { body: { output: { delivered: false, code: "503" } } }
    },
  })
  renderWithProviders(<NotificationsPage />)
  const user = userEvent.setup()
  const p = await card("ops-phone")
  await user.click(within(p).getByRole("button", { name: "Send test" }))
  expect(await within(p).findByText("Sending test…")).toBeTruthy()
  await waitFor(() => expect(release).not.toBeNull())
  release!()
  expect(await within(p).findByText("Test: Delivered")).toBeTruthy()
  const c = await card("team-chat")
  await user.click(within(c).getByRole("button", { name: "Send test" }))
  expect(await within(c).findByText("Test: Failed: 503")).toBeTruthy()
})

test("editing keeps stored secrets unless a new value is typed", async () => {
  const calls = stub([oncall], {
    [route("notify.channel.update", "plan")]: { body: makePlan("notify.channel.update", { confirmation: null }) },
  })
  renderWithProviders(<NotificationsPage />)
  const user = userEvent.setup()
  await user.click(within(await card("oncall")).getByRole("button", { name: "Actions for oncall" }))
  await user.click(await screen.findByRole("menuitem", { name: "Edit" }))
  const form = await screen.findByRole("dialog", { name: "Edit oncall" })
  expect(within(form).getByText("Saved — enter a new value to replace it.")).toBeTruthy()
  expect((within(form).getByLabelText("Password (optional)") as HTMLInputElement).value).toBe("")
  await user.click(within(form).getByRole("switch", { name: "Receives events" }))
  await user.click(within(form).getByRole("button", { name: "Review change" }))
  await screen.findByRole("dialog", { name: "Edit oncall" })
  await waitFor(() =>
    expect(calls.find((c) => c.path.endsWith("notify.channel.update/v1/plan"))?.body).toEqual({
      input: {
        id: "c3", name: "oncall", minSeverity: "critical", kinds: [], groupIds: ["g1", "g2"], enabled: false,
        email: { host: "smtp.example.com", port: 587, security: "starttls", username: "dp", from: "dp@example.com", recipients: ["a@example.com"] },
      },
    }),
  )
})

test("a failed load says so and retries", async () => {
  let fail = true
  stubFetch({
    [route("notify.channel.list", "read")]: () =>
      fail ? { status: 500, body: { type: "about:blank", title: "x", status: 500, detail: "boom", code: "internal" } } : { body: { output: { channels: [phone] } } },
    [route("core.group.list", "read")]: { body: { output: { groups } } },
  })
  renderWithProviders(<NotificationsPage />)
  expect(await screen.findByText("Couldn't load notification channels: boom")).toBeTruthy()
  fail = false
  await userEvent.setup().click(screen.getByRole("button", { name: "Retry" }))
  expect(await card("ops-phone")).toBeTruthy()
})
