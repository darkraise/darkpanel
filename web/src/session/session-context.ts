import { createContext } from "react"
import type { Session } from "@/api/types"

export interface SessionState {
  status: "loading" | "ready"
  session: Session | null
  // The session ended by a logout, so the login page gets no next parameter.
  loggedOut: boolean
}

export interface SessionContextValue extends SessionState {
  setSession: (session: Session | null) => void
  logout: () => Promise<void>
}

export const SessionContext = createContext<SessionContextValue | null>(null)
