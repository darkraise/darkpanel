import { screen, waitFor } from "@testing-library/react"
import type { QueryClient } from "@tanstack/react-query"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { PanelExposureBanner } from "./banner"
import { paExposure, paStatus } from "./fixtures"
import { panelStatusKey } from "./types"

const PB_READ = "POST /api/v1/actions/panel.exposure.status/v1/read"
const PB_TEXT = "Panel exposed via npm at panel.example.org"
const PB_UNREAD = "The panel exposure status could not be read"

function pbRender(response: StubHandler) {
  const calls = stubFetch({ [PB_READ]: response })
  const result = renderWithProviders(<PanelExposureBanner />)
  return { calls, ...result }
}

async function pbSettled(client: QueryClient) {
  await waitFor(() => expect(["success", "error"]).toContain(client.getQueryState(panelStatusKey)?.status))
}

test("says how the panel is exposed and links to the controller host's panel page to remove it", async () => {
  const { calls } = pbRender({ body: { output: paStatus(paExposure({ hostId: "host/1" })) } })
  expect(await screen.findByText(PB_TEXT)).toBeTruthy()
  expect(screen.getByRole("link", { name: "Remove the panel exposure" }).getAttribute("href")).toBe("/hosts/host%2F1/panel")
  expect(calls.filter((c) => c.path === "/api/v1/actions/panel.exposure.status/v1/read").map((c) => c.body)).toEqual([{ input: {} }])
})

test("names the tunnel route", async () => {
  pbRender({ body: { output: paStatus(paExposure({ via: "tunnel", tunnel: "0123abcd-0123-4abc-8def-0123456789ab" })) } })
  expect(await screen.findByText("Panel exposed via tunnel at panel.example.org")).toBeTruthy()
})

test.each([
  ["revoked", "The listener is revoked, but the removal has not finished."],
  ["needs_repair", "The exposure needs repair. Remove it again to finish."],
] as const)("an exposure in state %s says what is left to do", async (state, note) => {
  pbRender({ body: { output: paStatus(paExposure({ state })) } })
  expect(await screen.findByText(PB_TEXT)).toBeTruthy()
  expect(screen.getByText(note)).toBeTruthy()
})

test("an open exposure carries no note", async () => {
  pbRender({ body: { output: paStatus(paExposure()) } })
  await screen.findByText(PB_TEXT)
  expect(screen.queryByText(/removal has not finished|needs repair/)).toBeNull()
})

test("renders nothing while the panel is not exposed", async () => {
  const { client } = pbRender({ body: { output: paStatus() } })
  await pbSettled(client)
  expect(screen.queryByText(/Panel exposed/)).toBeNull()
  expect(screen.queryByRole("link")).toBeNull()
})

test("warns that the status is unknown when the status read fails", async () => {
  const { client } = pbRender({ status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } })
  await pbSettled(client)
  expect(client.getQueryState(panelStatusKey)?.status).toBe("error")
  expect(await screen.findByText(PB_UNREAD)).toBeTruthy()
  expect(screen.getByText("the database is busy")).toBeTruthy()
  expect(screen.queryByText(/Panel exposed/)).toBeNull()
  expect(screen.queryByRole("link")).toBeNull()
})

test("warns that the status is unknown when the controller answers with something that is not a status", async () => {
  const { client } = pbRender({ body: { output: { exposed: true } } })
  await pbSettled(client)
  expect(client.getQueryState(panelStatusKey)?.status).toBe("error")
  expect(await screen.findByText(PB_UNREAD)).toBeTruthy()
  expect(screen.getByText("The controller answered with an unexpected panel exposure status.")).toBeTruthy()
  expect(screen.queryByText(/Panel exposed/)).toBeNull()
})

test("the unknown-status warning has no accessibility violations", async () => {
  const { container } = pbRender({ status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } })
  await screen.findByText(PB_UNREAD)
  expect(await axe(container)).toHaveNoViolations()
})

test("the banner has no accessibility violations", async () => {
  const { container } = pbRender({ body: { output: paStatus(paExposure({ state: "needs_repair" })) } })
  await screen.findByText(PB_TEXT)
  expect(await axe(container)).toHaveNoViolations()
})
