import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { makeHost, makePlan, renderWithProviders, stubFetch } from "@/test/utils"
import { UpdatesTab } from "./updates-tab"
import { PackagesTab } from "./packages-tab"

const R = (name: string) => `GET /api/v1/hosts/h1/resources/system/${name}`
const host = makeHost({ id: "h1", modules: [{ id: "system", version: 1, enabled: true, available: true, reason: null, checkedAt: null }] })

test("UX-1: security first and preselected, removals and held back in their own groups, Upgrade selected sends the names", async () => {
  const calls = stubFetch({
    [R("updates")]: { body: { upgradable: [{ name: "curl", from: "1", to: "2", origin: "x-updates", security: false }, { name: "openssl", from: "1", to: "2", origin: "x-security", security: true }], removals: ["oldlib"], heldBack: [{ name: "systemd", version: "2", reason: "phased" }], refreshedAt: null, failedSources: [], dpkgInterrupted: false } },
    "POST /api/v1/hosts/h1/actions/system.packages.upgrade/v1/plan": { body: makePlan("system.packages.upgrade") },
  })
  renderWithProviders(<UpdatesTab host={host} />)
  const groups = await screen.findAllByRole("group")
  expect(groups.map((g) => g.getAttribute("aria-label"))).toEqual(["Security", "Other updates", "Will be removed", "Held back"])
  within(groups[3]!).getByText("systemd · phased")
  expect(within(groups[0]!).getAllByText("Security")).toHaveLength(2)
  screen.getByText("1 of 2 selected")
  expect(screen.getByRole("checkbox", { name: "openssl" }).getAttribute("aria-checked")).toBe("true")
  await userEvent.click(screen.getByRole("checkbox", { name: "curl" }))
  screen.getByText("2 of 2 selected")
  await userEvent.click(screen.getByRole("button", { name: "Upgrade selected" }))
  expect(calls.find((c) => c.path.endsWith("/plan"))?.body).toEqual({ input: { all: false, names: ["curl", "openssl"] } })
})

test("UX-3, A54: protected rows show a lock with the reason and no remove button", async () => {
  stubFetch({ [R("packages")]: { body: [{ name: "openssh-server", version: "1", arch: "amd64", description: "ssh", protected: "keeps remote access" }, { name: "jq", version: "1", arch: "amd64", description: "json", protected: null }] } })
  renderWithProviders(<PackagesTab host={host} />)
  const ssh = within(await screen.findByRole("row", { name: /openssh-server/ }))
  expect(ssh.queryByRole("button", { name: /Remove/ })).toBeNull()
  ssh.getByRole("img", { name: "Protected" })
  await userEvent.click(ssh.getByRole("button", { name: "Why openssh-server is protected" }))
  expect((await screen.findAllByText("keeps remote access")).length).toBeGreaterThan(0)
  within(screen.getByRole("row", { name: /jq/ })).getByRole("button", { name: "Remove jq" })
})

test("UX-2: search lists results with install buttons", async () => {
  const calls = stubFetch({
    [R("packages")]: { body: [] },
    "POST /api/v1/hosts/h1/actions/system.packages.search/v1/read": { body: { output: [{ name: "jq", candidate: "1.7", installed: false }] } },
  })
  renderWithProviders(<PackagesTab host={host} />)
  await userEvent.type(screen.getByRole("searchbox", { name: "Search packages" }), "jq")
  await screen.findByRole("button", { name: "Install jq" })
  expect(calls.some((c) => (c.body as { input?: { term?: string } })?.input?.term === "jq")).toBe(true)
})
