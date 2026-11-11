import { act, screen, waitFor, within } from "@testing-library/react"
import { useState } from "react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import { HOSTS_QUERY_KEY } from "@/api/client"
import type { Host } from "@/api/types"
import { makeChange, makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { HostRemoval } from "./host-removal"

const route = (action: string, op: string) => `POST /api/v1/actions/${action}/v1/${op}`

test("UX-3: revoking a host plans, takes the typed name and applies", async () => {
  const calls = stubFetch({
    [route("core.host.revoke", "plan")]: { body: makePlan("core.host.revoke", { confirmation: "web-1" }) },
    [route("core.host.revoke", "apply")]: { body: makeChange("core.host.revoke", makeHost({ state: "revoked" })) },
    [route("core.host.list", "read")]: { body: { output: { hosts: [] } } },
  })
  const { client } = renderWithProviders(<HostRemoval host={makeHost({ id: "h1", name: "web-1" })} />)
  const invalidate = vi.spyOn(client, "invalidateQueries")
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Revoke host" }))
  const dialog = await screen.findByRole("dialog")
  await user.type(await within(dialog).findByLabelText("Type web-1 to confirm"), "web-1")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  expect(invalidate).toHaveBeenCalledWith({ queryKey: HOSTS_QUERY_KEY })
  expect(calls.find((c) => c.path.endsWith("revoke/v1/plan"))?.body).toEqual({ input: { hostId: "h1" } })
  expect(calls.find((c) => c.path.endsWith("revoke/v1/apply"))?.body).toEqual({ planId: "p1", confirmation: "web-1" })
})

test("UX-3, UX-11: Escape closes the revoke dialog and returns focus to its button", async () => {
  stubFetch({ [route("core.host.revoke", "plan")]: { body: makePlan("core.host.revoke", { confirmation: "web-1" }) } })
  renderWithProviders(<HostRemoval host={makeHost({ name: "web-1" })} />)
  const user = userEvent.setup()
  const button = screen.getByRole("button", { name: "Revoke host" })
  await user.click(button)
  await screen.findByRole("dialog")
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  expect(document.activeElement).toBe(button)
})

test("UX-3, UX-4, UX-5: each control shows only for the host states it applies to", () => {
  const shown = (h: Parameters<typeof makeHost>[0]) => {
    const r = renderWithProviders(<HostRemoval host={makeHost(h)} />)
    const names = screen.queryAllByRole("button").map((b) => b.textContent)
    r.unmount()
    return names
  }
  expect(shown({ state: "enrolled" })).toEqual(["Revoke host"])
  expect(shown({ state: "pending" })).toEqual(["New enrollment token", "Revoke host", "Delete host"])
  expect(shown({ state: "revoked" })).toEqual(["New enrollment token", "Delete host"])
  expect(shown({ state: "enrolled", isLocal: true })).toEqual([])
})

test("UX-4: deleting a host applies and returns to the host list", async () => {
  stubFetch({
    [route("core.host.delete", "plan")]: { body: makePlan("core.host.delete", { confirmation: "old-1" }) },
    [route("core.host.delete", "apply")]: { body: makeChange("core.host.delete", makeHost({ name: "old-1" })) },
    [route("core.host.list", "read")]: { body: { output: { hosts: [] } } },
  })
  const { client } = renderWithProviders(<HostRemoval host={makeHost({ name: "old-1", state: "revoked" })} />, { path: "/hosts/h1" })
  const invalidate = vi.spyOn(client, "invalidateQueries")
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Delete host" }))
  const dialog = await screen.findByRole("dialog")
  await user.type(await within(dialog).findByLabelText("Type old-1 to confirm"), "old-1")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await waitFor(() => expect(screen.getByTestId("location").textContent).toBe("/"))
  expect(invalidate).toHaveBeenCalledWith({ queryKey: HOSTS_QUERY_KEY })
})

test("UX-5: a new token refuses a malformed address, then shows the token once", async () => {
  const grant = { host: makeHost({ name: "new-1" }), token: "dpe_example_token", expiresAt: "2026-11-11T09:00:00Z" }
  const calls = stubFetch({
    [route("core.host.token", "plan")]: { body: makePlan("core.host.token", { confirmation: "new-1" }) },
    [route("core.host.token", "apply")]: { body: makeChange("core.host.token", grant) },
  })
  renderWithProviders(<HostRemoval host={makeHost({ id: "h2", name: "new-1", state: "pending" })} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "New enrollment token" }))
  const address = await screen.findByLabelText("Controller address (optional)")
  await waitFor(() => expect(document.activeElement).toBe(address))
  await user.type(address, "not an address{Enter}")
  screen.getByText("Enter host:port, such as panel.example.com:7443.")
  expect(document.activeElement).toBe(address)
  await user.clear(address)
  await user.type(address, "10.0.0.1:7443")
  await user.click(screen.getByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  await user.type(await within(dialog).findByLabelText("Type new-1 to confirm"), "new-1")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  expect((await screen.findByLabelText("Enrollment token for new-1")) as HTMLInputElement).toHaveProperty("value", "dpe_example_token")
  expect(calls.find((c) => c.path.endsWith("token/v1/plan"))?.body).toEqual({ input: { hostId: "h2", controllerAddress: "10.0.0.1:7443" } })
})

test("UX-3, UX-11: closing the dialog after a revoke focuses the revoked host's controls", async () => {
  stubFetch({
    [route("core.host.revoke", "plan")]: { body: makePlan("core.host.revoke", { confirmation: "web-1" }) },
    [route("core.host.revoke", "apply")]: { body: makeChange("core.host.revoke", makeHost({ state: "revoked" })) },
    [route("core.host.list", "read")]: { body: { output: { hosts: [] } } },
  })
  let revoke = () => {}
  function Harness() {
    const [host, setHost] = useState<Host>(makeHost({ id: "h1", name: "web-1" }))
    revoke = () => setHost((h) => ({ ...h, state: "revoked" }))
    return <HostRemoval host={host} />
  }
  renderWithProviders(<Harness />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Revoke host" }))
  const dialog = await screen.findByRole("dialog")
  await user.type(await within(dialog).findByLabelText("Type web-1 to confirm"), "web-1")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await within(dialog).findByText("completed")
  act(() => revoke())
  await user.keyboard("{Escape}")
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("button", { name: "New enrollment token" })))
})

test("UX-3, UX-4: a failed revoke or delete neither refreshes the hosts nor leaves the page", async () => {
  stubFetch({
    [route("core.host.revoke", "plan")]: { body: makePlan("core.host.revoke", { confirmation: "old-1" }) },
    [route("core.host.revoke", "apply")]: { body: makeChange("core.host.revoke", null, { state: "failed", error: { code: "change.failed", message: "The controller refused the change." } }) },
    [route("core.host.delete", "plan")]: { body: makePlan("core.host.delete", { confirmation: "old-1" }) },
    [route("core.host.delete", "apply")]: { body: makeChange("core.host.delete", null, { state: "failed", error: { code: "change.failed", message: "The controller refused the change." } }) },
  })
  const { client } = renderWithProviders(<HostRemoval host={makeHost({ name: "old-1", state: "pending" })} />, { path: "/hosts/h1" })
  const invalidate = vi.spyOn(client, "invalidateQueries")
  const user = userEvent.setup()
  for (const name of ["Revoke host", "Delete host"]) {
    await user.click(screen.getByRole("button", { name }))
    const dialog = await screen.findByRole("dialog")
    await user.type(await within(dialog).findByLabelText("Type old-1 to confirm"), "old-1")
    await user.click(within(dialog).getByRole("button", { name: "Apply" }))
    await within(dialog).findByText("failed")
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
  }
  expect(invalidate).not.toHaveBeenCalled()
  expect(screen.getByTestId("location").textContent).toBe("/hosts/h1")
})

test("UX-5: a blank address plans the token without one, and a missing grant says so", async () => {
  const calls = stubFetch({
    [route("core.host.token", "plan")]: { body: makePlan("core.host.token", { confirmation: "new-1" }) },
    [route("core.host.token", "apply")]: { body: makeChange("core.host.token", { host: null }) },
  })
  renderWithProviders(<HostRemoval host={makeHost({ id: "h2", name: "new-1", state: "pending" })} />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "New enrollment token" }))
  await user.click(await screen.findByRole("button", { name: "Continue" }))
  const dialog = await screen.findByRole("dialog")
  await user.type(await within(dialog).findByLabelText("Type new-1 to confirm"), "new-1")
  await user.click(within(dialog).getByRole("button", { name: "Apply" }))
  await screen.findByText("The controller did not return the enrollment token.")
  expect(calls.find((c) => c.path.endsWith("token/v1/plan"))?.body).toEqual({ input: { hostId: "h2" } })
})
