import { useState } from "react"
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type FetchCall } from "@/test/utils"
import { DockerSources } from "./docker-sources"
import { fwDocker } from "./fixtures"
import type { DockerStatus, Exemption } from "./types"

const DS_PLAN = "POST /api/v1/hosts/h1/actions/firewall.docker.set-sources/v1/plan"

const dsNat: Exemption = { origDestination: "203.0.113.5", origPort: 8443, destination: "192.168.50.10", proto: "tcp", port: 443 }
const dsVpn: Exemption = { inInterface: "eth0", origDestination: "203.0.113.5", destination: "192.168.60.2", proto: "udp" }
const dsProposed: Exemption = { origDestination: "203.0.113.7", origPort: 2222, destination: "192.168.50.20", proto: "tcp", port: 22 }

function dsStatus(over: Partial<DockerStatus> = {}): DockerStatus {
  return fwDocker({ settings: { privateSources: ["10.0.0.0/8", "172.16.0.0/12"], forwardExemptions: [dsNat, dsVpn] }, foreignForwards: [], ...over })
}

function dsRender(docker: DockerStatus): { calls: FetchCall[]; container: HTMLElement } {
  const calls = stubFetch({
    "GET /api/v1/hosts/h1/modules/firewall/availability": { body: { module: "firewall", available: true, reason: null, actions: [] } },
    [DS_PLAN]: {
      body: { planId: "p1", hostId: "h1", action: "firewall.docker.set-sources", version: 1, inputHash: "h", diff: [{ op: "~", text: "sources" }], guards: [], expiresAt: "2026-10-28T09:00:00Z", confirmation: null },
    },
  })
  const { container } = renderWithProviders(<DockerSources hostId="h1" docker={docker} />)
  return { calls, container }
}

// dsPlanned waits for the change dialog titled title to show its plan and
// returns the input the plan call carried.
async function dsPlanned(calls: FetchCall[], title: string): Promise<unknown> {
  const dialog = await screen.findByRole("dialog", { name: title })
  await within(dialog).findByLabelText("Planned changes")
  const call = calls.find((c) => c.path.endsWith("/plan"))
  expect(call?.path).toBe("/api/v1/hosts/h1/actions/firewall.docker.set-sources/v1/plan")
  return call?.body
}

function dsAddForm(): HTMLElement {
  return screen.getByRole("form", { name: "Add forward exemption" })
}

test("plans the private source ranges typed one per line, without blanks and spaces", async () => {
  const { calls } = dsRender(dsStatus())
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Private sources" })
  const box = within(form).getByLabelText("Private source ranges")
  expect((box as HTMLTextAreaElement).value).toBe("10.0.0.0/8\n172.16.0.0/12")
  const submit = within(form).getByRole("button", { name: "Set private sources" })
  expect(submit.hasAttribute("disabled")).toBe(true)
  await user.clear(box)
  await user.type(box, "10.0.0.0/8{Enter}  192.168.0.0/16 {Enter}{Enter}fc00::/7")
  await user.click(submit)
  expect(await dsPlanned(calls, "Set the private sources")).toEqual({ input: { privateSources: ["10.0.0.0/8", "192.168.0.0/16", "fc00::/7"] } })
})

test("a status that changes under the form replaces the draft", async () => {
  function Swap() {
    const [docker, setDocker] = useState(dsStatus())
    return (
      <>
        <button onClick={() => setDocker(dsStatus({ settings: { privateSources: ["100.64.0.0/10"], forwardExemptions: [] } }))}>Refetch</button>
        <DockerSources hostId="h1" docker={docker} />
      </>
    )
  }
  stubFetch({})
  renderWithProviders(<Swap />)
  const user = userEvent.setup()
  const box = screen.getByLabelText("Private source ranges") as HTMLTextAreaElement
  await user.type(box, "{Enter}192.168.0.0/16")
  expect(box.value).toBe("10.0.0.0/8\n172.16.0.0/12\n192.168.0.0/16")
  await user.click(screen.getByRole("button", { name: "Refetch" }))
  expect((screen.getByLabelText("Private source ranges") as HTMLTextAreaElement).value).toBe("100.64.0.0/10")
  expect(screen.queryByRole("table", { name: "Forward exemptions" })).toBeNull()
  expect(screen.getByText("No forward exemptions.").textContent).toBe("No forward exemptions.")
})

test("lists the forward exemptions with any for what they leave open", () => {
  dsRender(dsStatus())
  const table = screen.getByRole("table", { name: "Forward exemptions" })
  const rows = within(table).getAllByRole("row").slice(1)
  expect(rows.map((r) => within(r).getAllByRole("cell").slice(0, 6).map((c) => c.textContent))).toEqual([
    ["any", "203.0.113.5", "8443", "192.168.50.10", "443", "tcp"],
    ["eth0", "203.0.113.5", "any", "192.168.60.2", "any", "udp"],
  ])
})

test("removing an exemption plans the list without it", async () => {
  const { calls } = dsRender(dsStatus())
  await userEvent.setup().click(screen.getByRole("button", { name: "Remove exemption 1" }))
  expect(await dsPlanned(calls, "Remove a forward exemption")).toEqual({ input: { forwardExemptions: [dsVpn] } })
})

test("removing the last exemption plans an empty list", async () => {
  const { calls } = dsRender(dsStatus({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [dsNat] } }))
  await userEvent.setup().click(screen.getByRole("button", { name: "Remove exemption 1" }))
  expect(await dsPlanned(calls, "Remove a forward exemption")).toEqual({ input: { forwardExemptions: [] } })
})

test("adds an exemption with every field to the list", async () => {
  const { calls } = dsRender(dsStatus())
  const user = userEvent.setup()
  const form = dsAddForm()
  await user.type(within(form).getByLabelText("In interface (optional)"), "eth1")
  await user.type(within(form).getByLabelText("Original destination"), "203.0.113.9")
  await user.type(within(form).getByLabelText("Original port (optional)"), "9000")
  await user.type(within(form).getByLabelText("Destination"), "192.168.70.4")
  await user.type(within(form).getByLabelText("Destination port (optional)"), "8080")
  await user.click(within(form).getByLabelText("udp"))
  await user.click(within(form).getByRole("button", { name: "Add exemption" }))
  expect(await dsPlanned(calls, "Add a forward exemption")).toEqual({
    input: {
      forwardExemptions: [dsNat, dsVpn, { inInterface: "eth1", origDestination: "203.0.113.9", origPort: 9000, destination: "192.168.70.4", proto: "udp", port: 8080 }],
    },
  })
})

test("an exemption with only its addresses is a tcp one that leaves the rest out", async () => {
  const { calls } = dsRender(dsStatus({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] } }))
  const user = userEvent.setup()
  const form = dsAddForm()
  await user.type(within(form).getByLabelText("Original destination"), " 203.0.113.9 ")
  await user.type(within(form).getByLabelText("Destination"), "192.168.70.4")
  await user.click(within(form).getByRole("button", { name: "Add exemption" }))
  expect(await dsPlanned(calls, "Add a forward exemption")).toEqual({
    input: { forwardExemptions: [{ origDestination: "203.0.113.9", destination: "192.168.70.4", proto: "tcp" }] },
  })
})

test.each([
  { name: "no addresses", orig: "", dest: "", port: "", message: "Enter the original destination and the destination." },
  { name: "no destination", orig: "203.0.113.9", dest: "  ", port: "", message: "Enter the original destination and the destination." },
  { name: "a port above 65535", orig: "203.0.113.9", dest: "192.168.70.4", port: "70000", message: "Ports are whole numbers from 1 to 65535." },
  { name: "port zero", orig: "203.0.113.9", dest: "192.168.70.4", port: "0", message: "Ports are whole numbers from 1 to 65535." },
  { name: "a port with letters", orig: "203.0.113.9", dest: "192.168.70.4", port: "80a", message: "Ports are whole numbers from 1 to 65535." },
])("a form with $name plans nothing and says why", async ({ orig, dest, port, message }) => {
  const { calls } = dsRender(dsStatus())
  const user = userEvent.setup()
  const form = dsAddForm()
  if (orig !== "") await user.type(within(form).getByLabelText("Original destination"), orig)
  if (dest !== "") await user.type(within(form).getByLabelText("Destination"), dest)
  if (port !== "") await user.type(within(form).getByLabelText("Original port (optional)"), port)
  await user.click(within(form).getByRole("button", { name: "Add exemption" }))
  expect(within(form).getByRole("alert").textContent).toBe(message)
  expect(calls.some((c) => c.path.endsWith("/plan"))).toBe(false)
  expect(screen.queryByRole("dialog")).toBeNull()
})

test("a valid exemption after an invalid one clears the message and plans", async () => {
  const { calls } = dsRender(dsStatus({ settings: { privateSources: ["10.0.0.0/8"], forwardExemptions: [] } }))
  const user = userEvent.setup()
  const form = dsAddForm()
  await user.type(within(form).getByLabelText("Original destination"), "203.0.113.9")
  await user.click(within(form).getByRole("button", { name: "Add exemption" }))
  expect(within(form).getByRole("alert").textContent).toBe("Enter the original destination and the destination.")
  await user.type(within(form).getByLabelText("Destination"), "192.168.70.4")
  await user.click(within(form).getByRole("button", { name: "Add exemption" }))
  expect(await dsPlanned(calls, "Add a forward exemption")).toEqual({
    input: { forwardExemptions: [{ origDestination: "203.0.113.9", destination: "192.168.70.4", proto: "tcp" }] },
  })
  expect(within(form).queryByRole("alert")).toBeNull()
})

test("clearing every private source plans an empty list", async () => {
  const { calls } = dsRender(dsStatus())
  const user = userEvent.setup()
  const form = screen.getByRole("form", { name: "Private sources" })
  await user.clear(within(form).getByLabelText("Private source ranges"))
  await user.click(within(form).getByRole("button", { name: "Set private sources" }))
  expect(await dsPlanned(calls, "Set the private sources")).toEqual({ input: { privateSources: [] } })
})

test("offers each proposed exemption, which adds it to the list", async () => {
  const { calls } = dsRender(dsStatus({ foreignForwards: [dsProposed] }))
  const table = screen.getByRole("table", { name: "Proposed forward exemptions" })
  expect(within(table).getAllByRole("row")[1]?.textContent).toContain("192.168.50.20")
  await userEvent.setup().click(screen.getByRole("button", { name: "Add proposed exemption 1" }))
  expect(await dsPlanned(calls, "Add a forward exemption")).toEqual({ input: { forwardExemptions: [dsNat, dsVpn, dsProposed] } })
})

test("no proposals, no proposal table", () => {
  dsRender(dsStatus())
  expect(screen.queryByRole("table", { name: "Proposed forward exemptions" })).toBeNull()
})

test("the sources editor has no accessibility violations", async () => {
  const { container } = dsRender(dsStatus({ foreignForwards: [dsProposed] }))
  expect(await axe(container)).toHaveNoViolations()
})
