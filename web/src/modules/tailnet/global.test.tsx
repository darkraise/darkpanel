import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { tnPolicy } from "./fixtures"
import TailnetGlobal from "./global"

// The pages of other files are replaced by a marker each; this file tests the routes and the links.
vi.mock("./account-panel", () => ({ AccountPanel: () => <p>account panel</p> }))
vi.mock("./devices-page", () => ({ DevicesPage: () => <p>devices page</p> }))
vi.mock("./keys-page", () => ({ KeysPage: () => <p>keys page</p> }))

function glRender(path: string) {
  stubFetch({ "POST /api/v1/actions/tailnet.policy.get/v1/read": { body: { output: tnPolicy({ text: "{}\n", etag: "e1" }) } } })
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
])("%s shows the %s", async (path, marker) => {
  glRender(path)
  expect((await screen.findByText(marker)).textContent).toBe(marker)
})

test("/tailnet/policy shows the policy editor", async () => {
  glRender("/tailnet/policy")
  expect(await screen.findByLabelText("Policy file (HuJSON)")).toBeTruthy()
})

test("an unknown address shows the not-found state", async () => {
  glRender("/tailnet/nowhere")
  expect((await screen.findByText("Page not found")).textContent).toBe("Page not found")
})

test("links the four pages and marks the open one", async () => {
  glRender("/tailnet/keys")
  const nav = await screen.findByRole("navigation", { name: "Tailnet pages" })
  const links = Array.from(nav.querySelectorAll("a")).map((a) => [a.textContent, a.getAttribute("href"), a.getAttribute("aria-current")])
  expect(links).toEqual([
    ["Account", "/tailnet", null],
    ["Devices", "/tailnet/devices", null],
    ["Auth keys", "/tailnet/keys", "page"],
    ["Policy", "/tailnet/policy", null],
  ])
})

test("following a link opens that page", async () => {
  glRender("/tailnet")
  const user = userEvent.setup()
  await user.click(await screen.findByRole("link", { name: "Devices" }))
  expect((await screen.findByText("devices page")).textContent).toBe("devices page")
  expect(screen.getByTestId("location").textContent).toBe("/tailnet/devices")
})
