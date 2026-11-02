import { screen } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { renderWithProviders } from "@/test/utils"
import DerpGlobal from "./global"

// The page of derp-page.tsx is replaced by a marker; this file tests the routes and the header.
vi.mock("./derp-page", () => ({ DerpPage: () => <p>derp page</p> }))

function glRender(path: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/derp/*" element={<DerpGlobal />} />
    </Routes>,
    { path },
  )
}

test("/derp shows the DERP title above the page", async () => {
  glRender("/derp")
  expect((await screen.findByRole("heading", { level: 1, name: "DERP" })).textContent).toBe("DERP")
  expect((await screen.findByText("derp page")).textContent).toBe("derp page")
})

test("an unknown address shows the not-found state and not the page", async () => {
  glRender("/derp/nowhere")
  expect((await screen.findByText("Page not found")).textContent).toBe("Page not found")
  expect(screen.queryByText("derp page")).toBeNull()
})
