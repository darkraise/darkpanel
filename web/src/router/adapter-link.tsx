import type { AnchorHTMLAttributes, Ref } from "react"
import { Link, NavLink, useLocation } from "react-router"
import type { RouterLinkProps } from "darkraise-ui/router"
import { activeByPrefix } from "./active-prefixes"

// darkraise-ui passes more than RouterLinkProps at run time (a tooltip
// trigger's handlers, ids and ref through asChild), so everything but the
// adapter's own props goes on to the anchor.
type AdapterLinkProps = RouterLinkProps & AnchorHTMLAttributes<HTMLAnchorElement> & { ref?: Ref<HTMLAnchorElement> }

// NavLink sets aria-current="page" on the active link, which darkraise-ui
// requires of every router adapter. NavLink always sets aria-current from its
// own match, so a link active by prefix is a plain Link that sets it itself.
export function AdapterLink({ to, className, activeClassName, activeExact, children, ...rest }: AdapterLinkProps) {
  const { pathname } = useLocation()
  const classes = (active: boolean) => [className, active ? activeClassName : undefined].filter(Boolean).join(" ") || undefined
  if (activeByPrefix(to, pathname)) {
    return (
      <Link {...rest} to={to} aria-current="page" className={classes(true)}>
        {children}
      </Link>
    )
  }
  return (
    <NavLink {...rest} to={to} end={activeExact} className={({ isActive }) => classes(isActive)}>
      {children}
    </NavLink>
  )
}
