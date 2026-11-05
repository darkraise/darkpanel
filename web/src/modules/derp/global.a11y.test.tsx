import { screen, waitFor } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { expectNoAxeViolations } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import DerpGlobal from "./global"

// The page of another file is replaced by a marker; this file tests the page's own title and landmarks.
vi.mock("./derp-page", () => ({ DerpPage: () => <p>derp page</p> }))

function gaRender(path: string) {
  stubFetch({})
  return renderWithProviders(
    <Routes>
      <Route path="/derp/*" element={<DerpGlobal />} />
    </Routes>,
    { path },
  )
}

test.each([
  ["/derp", "derp page"],
  ["/derp/nowhere", "Page not found"],
])("%s has the page title DERP and no axe violations", async (path, marker) => {
  const { container } = gaRender(path)
  await screen.findByText(marker)
  expect(document.title).toBe("DERP · Darkpanel")
  await expectNoAxeViolations(container)
})

test("the title is restored when the page unmounts", async () => {
  document.title = "Before"
  const { unmount } = gaRender("/derp")
  await waitFor(() => expect(document.title).toBe("DERP · Darkpanel"))
  unmount()
  expect(document.title).toBe("Before")
})
