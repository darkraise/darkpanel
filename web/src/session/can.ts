import { createContext, useContext, type ReactNode } from "react"
import type { Host, Scope, Session } from "@/api/types"

const RANK: Record<Scope | "", number> = { "": 0, read: 1, operate: 2, admin: 3 }

export function scopeAllows(have: Scope | "", need: Scope): boolean {
  return RANK[have] >= RANK[need]
}

// isAdmin reports admin access on everything: an admin user on a session or
// an admin token. An admin user's read or operate token is not admin here,
// as the server caps it the same way.
export function isAdmin(session: Session | null | undefined): boolean {
  return session?.user.allHosts === "admin"
}

export function canOnHost(host: Pick<Host, "scope">, need: Scope, session?: Session | null): boolean {
  return isAdmin(session) || scopeAllows(host.scope, need)
}

// HostScope is the caller's scope on the host a page is about. The host and
// change pages provide it; with no provider nothing is allowed, so a page
// that forgets to provide it shows no action rather than a refused one.
export const HostScope = createContext<Scope | "">("")

export function useCanOnHost(need: Scope): boolean {
  return scopeAllows(useContext(HostScope), need)
}

// Can renders its children only when the page's host scope allows need: an
// action the caller may not take has no button at all, never a disabled one.
export function Can({ need, children }: { need: Scope; children: ReactNode }): ReactNode {
  return useCanOnHost(need) ? children : null
}
