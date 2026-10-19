import { useContext } from "react"
import { SessionContext, type SessionContextValue } from "./session-context"

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext)
  if (value === null) throw new Error("useSession must be used inside SessionProvider")
  return value
}
