import type { AnchorHTMLAttributes, Ref } from "react"
import { NavLink } from "react-router"
import type { RouterLinkProps } from "darkraise-ui/router"

// darkraise-ui passes more than RouterLinkProps at run time (a tooltip
// trigger's handlers, ids and ref through asChild), so everything but the
// adapter's own props goes on to the anchor.
type AdapterLinkProps = RouterLinkProps & AnchorHTMLAttributes<HTMLAnchorElement> & { ref?: Ref<HTMLAnchorElement> }

// NavLink sets aria-current="page" on the active link, which darkraise-ui
// requires of every router adapter.
export function AdapterLink({ to, className, activeClassName, activeExact, children, ...rest }: AdapterLinkProps) {
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
