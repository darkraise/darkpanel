import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react"
import { Navigate, useLocation } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import { Spinner } from "darkraise-ui/components/spinner"
import { request, setCsrfToken, setUnauthorizedHandler } from "@/api/client"
import type { Session } from "@/api/types"

interface SessionState {
  status: "loading" | "ready"
  session: Session | null
}

interface SessionContextValue extends SessionState {
  setSession: (session: Session | null) => void
}

const SessionContext = createContext<SessionContextValue | null>(null)

export function SessionProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient()
  const [state, setState] = useState<SessionState>({ status: "loading", session: null })

  const setSession = useCallback(
    (session: Session | null) => {
      setCsrfToken(session?.csrfToken ?? null)
      if (session === null) client.clear()
      setState({ status: "ready", session })
    },
    [client],
  )

  useEffect(() => {
    let active = true
    setUnauthorizedHandler(() => {
      if (active) setSession(null)
    })
    request<Session>("GET", "/api/v1/auth/session").then(
      (session) => {
        if (active) setSession(session)
      },
      () => {
        if (active) setSession(null)
      },
    )
    return () => {
      active = false
      setUnauthorizedHandler(null)
    }
  }, [setSession])

  const value = useMemo(() => ({ ...state, setSession }), [state, setSession])
  return <SessionContext value={value}>{children}</SessionContext>
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext)
  if (value === null) throw new Error("useSession must be used inside SessionProvider")
  return value
}

// safeNext keeps a post-login redirect inside the app. Browsers drop tabs
// and newlines from URLs and read a backslash as a slash, so "/\t/host" and
// "/\host" name another origin; those characters are refused outright, and
// what is left must resolve to this origin.
export function safeNext(next: string | null): string {
  // eslint-disable-next-line no-control-regex
  if (next === null || !next.startsWith("/") || /[\u0000-\u001f\u007f\\]/.test(next)) return "/"
  let url: URL
  try {
    url = new URL(next, window.location.origin)
  } catch {
    return "/"
  }
  if (url.origin !== window.location.origin || url.pathname === "/login" || url.pathname.startsWith("/login/")) return "/"
  return url.pathname + url.search + url.hash
}

export function SessionSpinner() {
  return (
    <div className="flex justify-center py-16">
      <Spinner aria-label="Loading" />
    </div>
  )
}

export function RequireSession({ children }: { children: ReactNode }) {
  const { status, session } = useSession()
  const location = useLocation()
  if (status === "loading") return <SessionSpinner />
  if (session === null) {
    const next = location.pathname + location.search
    return <Navigate to={next === "/" ? "/login" : `/login?next=${encodeURIComponent(next)}`} replace />
  }
  return <>{children}</>
}
