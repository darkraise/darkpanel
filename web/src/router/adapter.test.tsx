import { createRef, type AnchorHTMLAttributes, type ComponentType, type Ref } from "react"
import { cleanup, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { QueryClient } from "@tanstack/react-query"
import { MemoryRouter } from "react-router"
import { useRouterAdapter, type RouterLinkProps } from "darkraise-ui/router"
import { afterEach, expect, test, vi } from "vitest"
import { Providers } from "@/providers"
import { routerAdapter } from "./adapter"

// The shared cleanup in test/setup.ts arrives with Task 5.
afterEach(cleanup)

// darkraise-ui passes props beyond RouterLinkProps at run time, for example
// a tooltip trigger's handlers and ref through asChild.
const ExtendedLink = routerAdapter.Link as ComponentType<
  RouterLinkProps & AnchorHTMLAttributes<HTMLAnchorElement> & { ref?: Ref<HTMLAnchorElement> }
>

function Links() {
  const { Link, usePathname, useNavigate } = useRouterAdapter()
  const pathname = usePathname()
  const navigate = useNavigate()
  return (
    <>
      <Link to="/" activeClassName="active" activeExact>Hosts</Link>
      <Link to="/audit" className="nav" activeClassName="active">Audit</Link>
      <button type="button" onClick={() => navigate("/settings/tokens")}>Go</button>
      <output data-testid="path">{pathname}</output>
    </>
  )
}

function renderAt(path: string) {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Providers client={new QueryClient()}>
        <Links />
      </Providers>
    </MemoryRouter>,
  )
}

test("the active link carries aria-current and the active class", () => {
  renderAt("/audit/older")
  const audit = screen.getByRole("link", { name: "Audit" })
  expect(audit.getAttribute("aria-current")).toBe("page")
  expect(audit.className).toBe("nav active")
  const hosts = screen.getByRole("link", { name: "Hosts" })
  expect(hosts.getAttribute("aria-current")).toBeNull()
  expect(hosts.getAttribute("href")).toBe("/")
})

test("activeExact matches only the exact path", () => {
  renderAt("/")
  expect(screen.getByRole("link", { name: "Hosts" }).getAttribute("aria-current")).toBe("page")
  expect(screen.getByRole("link", { name: "Audit" }).getAttribute("aria-current")).toBeNull()
})

test("useNavigate and usePathname follow the router", async () => {
  renderAt("/")
  await userEvent.setup().click(screen.getByRole("button", { name: "Go" }))
  expect(screen.getByTestId("path").textContent).toBe("/settings/tokens")
})

test("the link forwards every other prop and its ref to the anchor", async () => {
  const ref = createRef<HTMLAnchorElement>()
  const onFocus = vi.fn()
  render(
    <MemoryRouter initialEntries={["/"]}>
      <Providers client={new QueryClient()}>
        <ExtendedLink to="/audit" ref={ref} id="nav-audit" aria-describedby="tip-audit" onFocus={onFocus} data-state="closed">
          Audit
        </ExtendedLink>
      </Providers>
    </MemoryRouter>,
  )
  const link = screen.getByRole("link", { name: "Audit" })
  expect(ref.current).toBe(link)
  expect(link.id).toBe("nav-audit")
  expect(link.getAttribute("aria-describedby")).toBe("tip-audit")
  expect(link.getAttribute("data-state")).toBe("closed")
  await userEvent.setup().tab()
  expect(onFocus).toHaveBeenCalledTimes(1)
})
