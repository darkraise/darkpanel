import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react"
import { Navigate, useSearchParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { ApiError, OIDC_START_PATH, finishCodeStep, getSignInMethods, getTailscaleMatch, request, signInTailscale } from "@/api/client"
import type { Session, TailscaleMatch } from "@/api/types"
import { usePageTitle } from "@/lib/page-title"
import { SessionSpinner } from "@/session/session"
import { safeNext } from "@/session/safe-next"
import { useSession } from "@/session/use-session"

const GENERIC_ERROR = "Login failed. Check the username, password and code, then try again."
const CODE_ERROR = "That code did not work. Try the current one, or sign in again."
const METHOD_OFF = "This sign-in method is off. Use your username, password and code."
const REFUSED = "The sign-in was refused. Try again, or use your username, password and code."
const NOT_CODE_READY = "Continue as needs your authenticator code once. Finish account setup or sign in below."

// rememberLine names the node by the first label of its tailnet name.
function rememberLine(nodeName: string): string {
  const label = nodeName.split(".")[0]
  return `Enter your code once to remember ${label === "" ? "this device" : label}.`
}

// The outcomes the OIDC callback page navigates with (/?signin=<outcome>),
// but for ok and totp, which need no message.
const OUTCOME_MESSAGES: Record<string, string> = {
  no_linked_user: "No Darkpanel user is linked to this account. Ask an admin to add you.",
  method_off: METHOD_OFF,
  refused: REFUSED,
}

function limited(err: unknown): string | null {
  if (err instanceof ApiError && err.code === "auth.rate_limited") {
    return err.retryAfterSeconds === null ? "Too many attempts. Try again later." : `Too many attempts. Try again in ${err.retryAfterSeconds} seconds.`
  }
  if (err instanceof ApiError && err.code === "network") return err.message
  return null
}

// The login page shows one generic error, so it reveals nothing about which
// of the three fields was wrong (spec section 17).
function loginError(err: unknown): string {
  return limited(err) ?? GENERIC_ERROR
}

function stepError(err: unknown): string {
  if (err instanceof ApiError && err.code === "auth.method_off") return METHOD_OFF
  return limited(err) ?? CODE_ERROR
}

function tailscaleError(err: unknown): string {
  if (err instanceof ApiError && err.code === "auth.method_off") return METHOD_OFF
  return limited(err) ?? REFUSED
}

// useSignInOutcome reads the sign-in outcome once and drops it from the
// address, so a reload does not show it again.
function useSignInOutcome(): string | null {
  const [params, setParams] = useSearchParams()
  const [outcome] = useState(() => params.get("signin"))
  useEffect(() => {
    if (!params.has("signin")) return
    const next = new URLSearchParams(params)
    next.delete("signin")
    setParams(next, { replace: true })
  }, [params, setParams])
  return outcome
}

function Frame({ children }: { children: ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="flex flex-col items-center gap-3 text-center">
          <span role="img" aria-label="Darkpanel" className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl text-xl font-semibold">
            <span aria-hidden>D</span>
          </span>
          <CardTitle>
            <h1 className="text-xl font-medium">Sign in</h1>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">{children}</CardContent>
      </Card>
    </main>
  )
}

function ErrorAlert({ message }: { message: string | null }) {
  if (message === null) return null
  return (
    <Alert variant="destructive" role="alert">
      <AlertDescription>{message}</AlertDescription>
    </Alert>
  )
}

// CodeStep finishes an OIDC or Tailscale sign-in that waits for the
// authenticator code; the session exists only after it.
function CodeStep({ username, remember, onSession, onBack }: { username: string | null; remember: string | null; onSession: (s: Session) => void; onBack: () => void }) {
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const code = String(new FormData(event.currentTarget).get("code") ?? "")
    setBusy(true)
    setError(null)
    try {
      onSession(await finishCodeStep(code))
    } catch (err) {
      setError(stepError(err))
      setBusy(false)
    }
  }

  return (
    <>
      <p className="text-sm text-muted-foreground">
        {username === null ? "Enter the code from your authenticator app to finish signing in." : `Enter the code from your authenticator app to finish signing in as ${username}.`}
      </p>
      {remember !== null && <p className="text-sm text-muted-foreground">{remember}</p>}
      <form className="flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
        <div className="flex flex-col gap-2">
          <Label htmlFor="step-code">Authentication code</Label>
          <Input id="step-code" name="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required autoFocus />
        </div>
        <ErrorAlert message={error} />
        <Button type="submit" loading={busy}>
          Log in
        </Button>
        <Button type="button" variant="ghost" onClick={onBack}>
          Use another way to sign in
        </Button>
      </form>
    </>
  )
}

export function LoginPage() {
  const { status, session, setSession } = useSession()
  const [params] = useSearchParams()
  const outcome = useSignInOutcome()
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [step, setStep] = useState<{ username: string | null; remember: string | null } | null>(() => (outcome === "totp" ? { username: null, remember: null } : null))
  const [continuing, setContinuing] = useState(false)
  const notice = outcome !== null && Object.prototype.hasOwnProperty.call(OUTCOME_MESSAGES, outcome) ? OUTCOME_MESSAGES[outcome] : undefined
  const noticeRef = useRef<HTMLDivElement>(null)
  const next = safeNext(params.get("next"))
  usePageTitle("Sign in")

  const signedOut = status !== "loading" && session === null
  const methods = useQuery({ queryKey: ["sign-in-methods"], queryFn: getSignInMethods, enabled: signedOut, retry: false, refetchOnWindowFocus: false })
  const match = useQuery({
    queryKey: ["tailscale-match"],
    queryFn: getTailscaleMatch,
    enabled: signedOut && methods.data?.tailscale === true,
    retry: false,
    refetchOnWindowFocus: false,
  })

  useEffect(() => {
    if (signedOut && notice !== undefined) noticeRef.current?.focus()
  }, [signedOut, notice])

  if (status === "loading") return <SessionSpinner />
  if (session !== null) return <Navigate to={next} replace />

  if (step !== null) {
    return (
      <Frame>
        <CodeStep username={step.username} remember={step.remember} onSession={setSession} onBack={() => setStep(null)} />
      </Frame>
    )
  }

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

  async function continueAs(who: TailscaleMatch) {
    setContinuing(true)
    setError(null)
    try {
      const res = await signInTailscale()
      if ("totp" in res) {
        setStep({ username: who.username, remember: res.remember ? rememberLine(who.nodeName) : null })
        return
      }
      setSession(res)
    } catch (err) {
      setError(tailscaleError(err))
    } finally {
      setContinuing(false)
    }
  }

  const who = match.data
  return (
    <Frame>
      {notice !== undefined && (
        <Alert ref={noticeRef} tabIndex={-1} variant="destructive" role="alert" className="focus-visible:outline-2 focus-visible:outline-ring">
          <AlertDescription>{notice}</AlertDescription>
        </Alert>
      )}
      {who !== undefined &&
        (who.remembered || who.codeReady ? (
          <Button type="button" variant="secondary" className="h-auto min-h-(--btn-height) whitespace-normal" loading={continuing} onClick={() => void continueAs(who)}>
            {`Continue as ${who.username} (${who.loginName}, from ${who.nodeName})`}
          </Button>
        ) : (
          <p className="text-sm text-muted-foreground">{NOT_CODE_READY}</p>
        ))}
      <form className="flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
        <div className="flex flex-col gap-2">
          <Label htmlFor="login-username">Username</Label>
          <Input id="login-username" name="username" autoComplete="username" required autoFocus={notice === undefined} />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="login-password">Password</Label>
          <Input id="login-password" name="password" type="password" autoComplete="current-password" required />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="login-code">Authentication code</Label>
          <Input id="login-code" name="code" inputMode="numeric" autoComplete="one-time-code" pattern="[0-9]{6}" maxLength={6} required />
        </div>
        <ErrorAlert message={error} />
        <Button type="submit" loading={busy}>
          Log in
        </Button>
      </form>
      {methods.data?.oidc != null && (
        <Button asChild variant="outline">
          <a href={OIDC_START_PATH}>{`Sign in with ${methods.data.oidc.displayName}`}</a>
        </Button>
      )}
    </Frame>
  )
}
