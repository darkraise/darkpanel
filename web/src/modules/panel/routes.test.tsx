import { screen } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import PanelRoutes from "./routes"

function prRender(path: string) {
  const calls = stubFetch({
    "POST /api/v1/actions/panel.exposure.status/v1/read": { body: { output: { exposed: false } } },
    "POST /api/v1/actions/core.host.list/v1/read": {
      body: { output: { hosts: [{ id: "h1", name: "controller", isLocal: true, online: true, modules: [], ingestion: null }] } },
    },
  })
  renderWithProviders(
    <Routes>
      <Route path="hosts/:hostId/panel/*" element={<PanelRoutes hostId="h1" />} />
    </Routes>,
    { path },
  )
  return calls
}

test("the index route is the panel page of the host", async () => {
  prRender("/hosts/h1/panel")
  expect(await screen.findByRole("form", { name: "Expose the panel" })).toBeTruthy()
})

test("any other path says the module has no such page and reads nothing", () => {
  const calls = prRender("/hosts/h1/panel/nowhere")
  expect(screen.getByText("Page not found")).toBeTruthy()
  expect(screen.getByText("The panel module has no page at this address.")).toBeTruthy()
  expect(calls).toEqual([])
})
