import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { Navigate, useLocation } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import { Spinner } from "darkraise-ui/components/spinner"
import { ApiError, request, sessionGeneration, setCsrfRejectedHandler, setCsrfToken, setUnauthorizedHandler } from "@/api/client"
import type { Session } from "@/api/types"
import { SessionContext, type SessionState } from "./session-context"
import { useSession } from "./use-session"

export function SessionProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient()
  const [state, setState] = useState<SessionState>({ status: "loading", session: null, loggedOut: false })
  const loggingOut = useRef(false)

  const replaceSession = useCallback(
    (session: Session | null, loggedOut: boolean) => {
      setCsrfToken(session?.csrfToken ?? null)
      if (session === null) client.clear()
      setState({ status: "ready", session, loggedOut })
    },
    [client],
  )

  const setSession = useCallback((session: Session | null) => replaceSession(session, false), [replaceSession])

  // Only a logout the server confirmed ends the local session: the session
  // cookie is HttpOnly, so after a failure the browser would still be logged
  // in on reload. An auth.required answer means the session had already
  // ended; the unauthorized handler ends it here too, as a logout.
  const logout = useCallback(async () => {
    loggingOut.current = true
    try {
      await request<void>("POST", "/api/v1/auth/logout")
      replaceSession(null, true)
    } catch (err) {
      if (!(err instanceof ApiError && err.code === "auth.required")) throw err
    } finally {
      loggingOut.current = false
    }
  }, [replaceSession])

  useEffect(() => {
    let active = true
    // The generation the running probe started in, or null when none runs.
    // A probe from an earlier generation has its answer discarded, so it must
    // not hold off the re-probe that a newer session's CSRF rejection needs.
    let probingIn: number | null = null
    function probe() {
      const startedIn = sessionGeneration()
      probingIn = startedIn
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
          if (probingIn === startedIn) probingIn = null
        })
    }
    setUnauthorizedHandler(() => {
      if (active) replaceSession(null, loggingOut.current)
    })
    // Another tab that logs in again replaces the session cookie, so this
    // tab's CSRF token goes stale and every unsafe request answers auth.csrf.
    // Probing the session fetches the token that belongs to the cookie.
    setCsrfRejectedHandler(() => {
      if (active && probingIn !== sessionGeneration()) probe()
    })
    probe()
    return () => {
      active = false
      setUnauthorizedHandler(null)
      setCsrfRejectedHandler(null)
    }
  }, [setSession, replaceSession])

  const value = useMemo(() => ({ ...state, setSession, logout }), [state, setSession, logout])
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
  const { status, session, loggedOut } = useSession()
  const location = useLocation()
  if (status === "loading") return <SessionSpinner />
  if (session === null) {
    const next = location.pathname + location.search
    return <Navigate to={loggedOut || next === "/" ? "/login" : `/login?next=${encodeURIComponent(next)}`} replace />
  }
  return <>{children}</>
}
