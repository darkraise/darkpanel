import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { expectNoAxeViolations, tabSequence } from "@/test/a11y"
import { renderWithProviders, stubFetch } from "@/test/utils"
import CloudflareGlobal from "./global"

// The pages of other files are replaced by a marker each; this file tests the page's own title, landmarks and keyboard order.
vi.mock("./account-panel", () => ({ AccountPanel: () => <p>account panel</p> }))
vi.mock("./tunnels-page", () => ({ TunnelsPage: () => <p>tunnels page</p> }))
vi.mock("./connectors-page", () => ({ ConnectorsPage: () => <p>connectors page</p> }))
vi.mock("./hostnames-page", () => ({ HostnamesPage: () => <p>hostnames page</p> }))

function gaRender(path: string) {
  stubFetch({})
  return renderWithProviders(
    <Routes>
      <Route path="/cloudflare/*" element={<CloudflareGlobal />} />
    </Routes>,
    { path },
  )
}

test.each([
  ["/cloudflare", "account panel"],
  ["/cloudflare/tunnels", "tunnels page"],
  ["/cloudflare/connectors", "connectors page"],
  ["/cloudflare/hostnames", "hostnames page"],
  ["/cloudflare/nowhere", "Page not found"],
])("%s has the page title Cloudflare and no axe violations", async (path, marker) => {
  const { container } = gaRender(path)
  await screen.findByText(marker)
  expect(document.title).toBe("Cloudflare · Darkpanel")
  await expectNoAxeViolations(container)
})

test("the title is restored when the page unmounts", async () => {
  document.title = "Before"
  const { unmount } = gaRender("/cloudflare")
  await waitFor(() => expect(document.title).toBe("Cloudflare · Darkpanel"))
  unmount()
  expect(document.title).toBe("Before")
})

test("Tab reaches the four page links in order, named by their text", async () => {
  gaRender("/cloudflare")
  await screen.findByText("account panel")
  expect(await tabSequence(userEvent.setup(), 4)).toEqual(["Account", "Tunnels", "Connectors", "Hostnames"])
})
