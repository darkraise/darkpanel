import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react"
import { Navigate, useLocation } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import { Spinner } from "darkraise-ui/components/spinner"
import { request, sessionGeneration, setCsrfRejectedHandler, setCsrfToken, setUnauthorizedHandler } from "@/api/client"
import type { Session } from "@/api/types"
import { SessionContext, type SessionState } from "./session-context"
import { useSession } from "./use-session"

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
    let probing = false
    function probe() {
      probing = true
      const startedIn = sessionGeneration()
      request<Session>("GET", "/api/v1/auth/session")
        .then(
          (session) => {
            if (active && startedIn === sessionGeneration()) setSession(session)
          },
          () => {
            if (active && startedIn === sessionGeneration()) setSession(null)
          },
        )
        .finally(() => {
          probing = false
        })
    }
    setUnauthorizedHandler(() => {
      if (active) setSession(null)
    })
    // Another tab that logs in again replaces the session cookie, so this
    // tab's CSRF token goes stale and every unsafe request answers auth.csrf.
    // Probing the session fetches the token that belongs to the cookie.
    setCsrfRejectedHandler(() => {
      if (active && !probing) probe()
    })
    probe()
    return () => {
      active = false
      setUnauthorizedHandler(null)
      setCsrfRejectedHandler(null)
    }
  }, [setSession])

  const value = useMemo(() => ({ ...state, setSession }), [state, setSession])
  return <SessionContext value={value}>{children}</SessionContext>
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
