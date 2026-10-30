import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import CloudflareGlobal from "./global"

// The pages of other files are replaced by a marker each; this file tests the routes and the links.
vi.mock("./account-panel", () => ({ AccountPanel: () => <p>account panel</p> }))
vi.mock("./tunnels-page", () => ({ TunnelsPage: () => <p>tunnels page</p> }))
vi.mock("./hostnames-page", () => ({ HostnamesPage: () => <p>hostnames page</p> }))

function glRender(path: string) {
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
  ["/cloudflare/hostnames", "hostnames page"],
])("%s shows the %s", async (path, marker) => {
  glRender(path)
  expect((await screen.findByText(marker)).textContent).toBe(marker)
})

test("an unknown address shows the not-found state", async () => {
  glRender("/cloudflare/nowhere")
  expect((await screen.findByText("Page not found")).textContent).toBe("Page not found")
})

test("links the three pages and marks the open one", async () => {
  glRender("/cloudflare/tunnels")
  const nav = await screen.findByRole("navigation", { name: "Cloudflare pages" })
  const links = Array.from(nav.querySelectorAll("a")).map((a) => [a.textContent, a.getAttribute("href"), a.getAttribute("aria-current")])
  expect(links).toEqual([
    ["Account", "/cloudflare", null],
    ["Tunnels", "/cloudflare/tunnels", "page"],
    ["Hostnames", "/cloudflare/hostnames", null],
  ])
})

test("following a link opens that page", async () => {
  glRender("/cloudflare")
  const user = userEvent.setup()
  await user.click(await screen.findByRole("link", { name: "Hostnames" }))
  expect((await screen.findByText("hostnames page")).textContent).toBe("hostnames page")
  expect(screen.getByTestId("location").textContent).toBe("/cloudflare/hostnames")
})
