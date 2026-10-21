import type { AnchorHTMLAttributes, Ref } from "react"
import { Link, matchPath, useLocation } from "react-router"
import type { RouterLinkProps } from "darkraise-ui/router"
import { activeByPrefix } from "./active-prefixes"

// darkraise-ui passes more than RouterLinkProps at run time (a tooltip
// trigger's handlers, ids and ref through asChild), so everything but the
// adapter's own props goes on to the anchor.
type AdapterLinkProps = RouterLinkProps & AnchorHTMLAttributes<HTMLAnchorElement> & { ref?: Ref<HTMLAnchorElement> }

// "/" is always exact, as in NavLink: a prefix match on it would mark every page.
function matchesOwnPath(to: string, pathname: string, exact: boolean | undefined): boolean {
  return matchPath({ path: to, end: exact === true || to === "/" }, pathname) !== null
}

// Always one Link, never a switch to NavLink: changing the element type
// remounts the anchor, and a keyboard user activating it would lose focus.
// darkraise-ui requires aria-current="page" on the active link.
export function AdapterLink({ to, className, activeClassName, activeExact, children, ...rest }: AdapterLinkProps) {
  const { pathname } = useLocation()
  const active = matchesOwnPath(to, pathname, activeExact) || activeByPrefix(to, pathname)
  const classes = [className, active ? activeClassName : undefined].filter(Boolean).join(" ") || undefined
  return (
    <Link {...rest} to={to} aria-current={active ? "page" : undefined} className={classes}>
      {children}
    </Link>
  )
}
