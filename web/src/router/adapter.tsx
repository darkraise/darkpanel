import type { AnchorHTMLAttributes, Ref } from "react"
import { NavLink, useLocation, useNavigate } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import type { RouterAdapter, RouterLinkProps } from "darkraise-ui/router"

// darkraise-ui passes more than RouterLinkProps at run time (a tooltip
// trigger's handlers, ids and ref through asChild), so everything but the
// adapter's own props goes on to the anchor.
type AdapterLinkProps = RouterLinkProps & AnchorHTMLAttributes<HTMLAnchorElement> & { ref?: Ref<HTMLAnchorElement> }

// NavLink sets aria-current="page" on the active link, which darkraise-ui
// requires of every router adapter.
function AdapterLink({ to, className, activeClassName, activeExact, children, ...rest }: AdapterLinkProps) {
  return (
    <NavLink
      {...rest}
      to={to}
      end={activeExact}
      className={({ isActive }) => [className, isActive ? activeClassName : undefined].filter(Boolean).join(" ") || undefined}
    >
      {children}
    </NavLink>
  )
}

function useAdapterNavigate(): (to: string) => void {
  const navigate = useNavigate()
  return (to) => {
    void navigate(to)
  }
}

function useAdapterPathname(): string {
  return useLocation().pathname
}

function useAdapterBack(): () => void {
  const navigate = useNavigate()
  return () => {
    void navigate(-1)
  }
}

function useAdapterInvalidate(): () => void {
  const client = useQueryClient()
  return () => {
    void client.invalidateQueries()
  }
}

export const routerAdapter: RouterAdapter = {
  Link: AdapterLink,
  useNavigate: useAdapterNavigate,
  usePathname: useAdapterPathname,
  useBack: useAdapterBack,
  useInvalidate: useAdapterInvalidate,
}
