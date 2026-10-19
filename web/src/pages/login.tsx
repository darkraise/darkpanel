import { useState, type FormEvent } from "react"
import { Navigate, useSearchParams } from "react-router"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { ApiError, request } from "@/api/client"
import type { Session } from "@/api/types"
import { SessionSpinner, safeNext, useSession } from "@/session/session"

const GENERIC_ERROR = "Login failed. Check the username, password and code, then try again."

// The login page shows one generic error, so it reveals nothing about which
// of the three fields was wrong (spec section 17).
function loginError(err: unknown): string {
  if (err instanceof ApiError && err.code === "auth.rate_limited") {
    return err.retryAfterSeconds === null
      ? "Too many attempts. Try again later."
      : `Too many attempts. Try again in ${err.retryAfterSeconds} seconds.`
  }
  if (err instanceof ApiError && err.code === "network") return err.message
  return GENERIC_ERROR
}

export function LoginPage() {
  const { status, session, setSession } = useSession()
  const [params] = useSearchParams()
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const next = safeNext(params.get("next"))

  if (status === "loading") return <SessionSpinner />
  if (session !== null) return <Navigate to={next} replace />

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError(null)
    try {
      setSession(
        await request<Session>("POST", "/api/v1/auth/login", {
          username: String(form.get("username") ?? ""),
          password: String(form.get("password") ?? ""),
          code: String(form.get("code") ?? ""),
        }),
      )
    } catch (err) {
      setError(loginError(err))
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>
            <h1 className="text-xl font-medium">Darkpanel</h1>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <form className="flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="login-username">Username</Label>
              <Input id="login-username" name="username" autoComplete="username" required autoFocus />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="login-password">Password</Label>
              <Input id="login-password" name="password" type="password" autoComplete="current-password" required />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="login-code">Authentication code</Label>
              <Input
                id="login-code"
                name="code"
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]{6}"
                maxLength={6}
                required
              />
            </div>
            {error !== null && (
              <Alert variant="destructive" role="alert">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <Button type="submit" loading={busy}>
              Log in
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
