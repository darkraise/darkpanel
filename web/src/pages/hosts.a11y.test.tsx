import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import type { Host } from "@/api/types"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
import { HostsPage } from "./hosts"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"

const hosts: Host[] = [
  {
    id: "h1", name: "web-1", isLocal: true, online: true,
    modules: [
      { id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null },
      { id: "docker", version: 1, enabled: true, available: false, reason: "docker is not installed", checkedAt: null },
      { id: "ufw", version: 1, enabled: false, available: true, reason: null, checkedAt: null },
    ],
    ingestion: { ok: true, since: "2026-10-21T08:00:00Z", error: null },
  },
  {
    id: "h2", name: "db-1", isLocal: false, online: false, modules: [],
    ingestion: { ok: false, since: "2026-10-21T08:05:00Z", error: "agent unreachable" },
  },
]

test("the host list passes axe with local, offline and failing-ingestion hosts", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  const { container } = renderWithProviders(<HostsPage />)
  await screen.findByRole("link", { name: "web-1" })
  await expectNoAxeViolations(container)
})

test("the host list passes axe while it loads", async () => {
  stubFetch({ [HOSTS]: () => new Promise<StubResponse>(() => undefined) })
  const { container } = renderWithProviders(<HostsPage />)
  await screen.findByLabelText("Loading the hosts")
  await expectNoAxeViolations(container)
})

test("the empty host list passes axe", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts: [] } } } })
  const { container } = renderWithProviders(<HostsPage />)
  await screen.findByText("No hosts yet")
  await expectNoAxeViolations(container)
})

test("the host list error passes axe", async () => {
  stubFetch({ [HOSTS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } } })
  const { container } = renderWithProviders(<HostsPage />)
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  await expectNoAxeViolations(container)
})

test("the Add host dialog passes axe", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  renderWithProviders(<HostsPage />)
  await waitFor(() => expect(screen.getByRole("button", { name: "Add host" }).hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(screen.getByRole("button", { name: "Add host" }))
  await expectNoAxeViolations(await screen.findByRole("dialog", { name: "Add a host" }))
})

test("the host list has one level-one heading, the page title and a table with named columns", async () => {
  stubFetch({ [HOSTS]: { body: { output: { hosts } } } })
  renderWithProviders(<HostsPage />)
  await screen.findByRole("link", { name: "web-1" })
  expect(screen.getAllByRole("heading", { level: 1 }).map((h) => h.textContent)).toEqual(["Hosts"])
  expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Status", "Modules"])
  expect(document.title).toBe("Hosts · Darkpanel")
})
