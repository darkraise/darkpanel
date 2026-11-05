import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import TailnetGlobal from "./global"

// The pages of other files are replaced by a marker each; this file tests the page's own title, landmarks and keyboard order.
vi.mock("./account-panel", () => ({ AccountPanel: () => <p>account panel</p> }))
vi.mock("./devices-page", () => ({ DevicesPage: () => <p>devices page</p> }))
vi.mock("./keys-page", () => ({ KeysPage: () => <p>keys page</p> }))
vi.mock("./policy-page", () => ({ PolicyPage: () => <p>policy page</p> }))

function gaRender(path: string) {
  stubFetch({})
  return renderWithProviders(
    <Routes>
      <Route path="/tailnet/*" element={<TailnetGlobal />} />
    </Routes>,
    { path },
  )
}

test.each([
  ["/tailnet", "account panel"],
  ["/tailnet/devices", "devices page"],
  ["/tailnet/keys", "keys page"],
  ["/tailnet/policy", "policy page"],
  ["/tailnet/nowhere", "Page not found"],
])("%s has the page title Tailnet and no axe violations", async (path, marker) => {
  const { container } = gaRender(path)
  await screen.findByText(marker)
  expect(document.title).toBe("Tailnet · Darkpanel")
  await expectNoAxeViolations(container)
})

test("the title is restored when the page unmounts", async () => {
  document.title = "Before"
  const { unmount } = gaRender("/tailnet")
  await waitFor(() => expect(document.title).toBe("Tailnet · Darkpanel"))
  unmount()
  expect(document.title).toBe("Before")
})

test("Tab reaches the four page links in order, named by their text", async () => {
  gaRender("/tailnet")
  await screen.findByText("account panel")
  expect(await tabSequence(userEvent.setup(), 4)).toEqual(["Account", "Devices", "Auth keys", "Policy"])
})
