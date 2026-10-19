import { createContext } from "react"
import type { Session } from "@/api/types"

export interface SessionState {
  status: "loading" | "ready"
  session: Session | null
}

export interface SessionContextValue extends SessionState {
  setSession: (session: Session | null) => void
}

export const SessionContext = createContext<SessionContextValue | null>(null)
