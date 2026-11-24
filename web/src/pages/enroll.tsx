import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react"
import { useLocation, useNavigate } from "react-router"
import { useQuery } from "@tanstack/react-query"
import qrcode from "qrcode-generator"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { ApiError, beginEnrollment, errorMessage, finishEnrollment } from "@/api/client"
import { focusFirstInvalid } from "@/lib/focus-invalid"
import { usePageTitle } from "@/lib/page-title"
import { useSession } from "@/session/use-session"

const MIN_PASSWORD = 12
const EXPIRED = "This link has expired. Ask an admin for a new one."

function expired(err: unknown): boolean {
  return err instanceof ApiError && (err.code === "auth.enrollment_expired" || err.code === "request.not_found")
}

// The login page's layout, so the link opens on a familiar page.
function EnrollFrame({ children }: { children: ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center p-4">
      <Card className="w-full max-w-sm">
        <CardHeader className="flex flex-col items-center gap-3 text-center">
          <span role="img" aria-label="Darkpanel" className="bg-primary text-primary-foreground flex size-12 items-center justify-center rounded-xl text-xl font-semibold">
            <span aria-hidden>D</span>
          </span>
          <CardTitle>
            <h1 className="text-xl font-medium">Set up your account</h1>
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-4">{children}</CardContent>
      </Card>
    </main>
  )
}

// QrCode draws the otpauth URI as modules of an SVG path. Scanners need dark
// modules on a light ground, so it keeps white and black in both themes.
function QrCode({ value }: { value: string }) {
  const { size, path } = useMemo(() => {
    const qr = qrcode(0, "M")
    qr.addData(value)
    qr.make()
    const n = qr.getModuleCount()
    let d = ""
    for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (qr.isDark(r, c)) d += `M${c + 4} ${r + 4}h1v1h-1z`
    return { size: n + 8, path: d }
  }, [value])
  return (
    <svg role="img" aria-label="QR code for your authenticator app" viewBox={`0 0 ${size} ${size}`} className="size-44 self-center rounded-md bg-white text-black">
      <path d={path} fill="currentColor" />
    </svg>
  )
}

function groupKey(secret: string): string {
  return secret.replace(/(.{4})(?=.)/g, "$1 ")
}

// useFragmentToken takes the token from the fragment once and drops it from
// the address bar, so it stays out of the history and of any copied address.
function useFragmentToken(): string {
  const location = useLocation()
  const navigate = useNavigate()
  const [token] = useState(() => location.hash.replace(/^#/, ""))
  useEffect(() => {
    if (location.hash !== "") void navigate({ pathname: location.pathname, search: location.search }, { replace: true })
  }, [location, navigate])
  return token
}

export function EnrollPage() {
  usePageTitle("Set up your account")
  const token = useFragmentToken()
  const navigate = useNavigate()
  const { setSession } = useSession()
  const start = useQuery({
    queryKey: ["enroll", token],
    queryFn: () => beginEnrollment(token),
    enabled: token !== "",
    retry: false,
    staleTime: Infinity,
    gcTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [code, setCode] = useState("")
  const [shown, setShown] = useState<{ password?: string; confirm?: string; form?: string }>({})
  const [busy, setBusy] = useState(false)
  const [gone, setGone] = useState(false)

  if (token === "" || gone || (start.isError && expired(start.error))) {
    return (
      <EnrollFrame>
        <Alert variant="destructive" role="alert">
          <AlertDescription>{EXPIRED}</AlertDescription>
        </Alert>
      </EnrollFrame>
    )
  }
  if (start.isPending) {
    return (
      <EnrollFrame>
        <Spinner aria-label="Opening the link" className="self-center" />
      </EnrollFrame>
    )
  }
  if (start.isError) {
    return (
      <EnrollFrame>
        <Alert variant="destructive" role="alert">
          <AlertDescription className="flex flex-col items-start gap-2">
            <span>{`Couldn't open the link: ${errorMessage(start.error)}`}</span>
            <Button size="sm" variant="outline" onClick={() => void start.refetch()}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      </EnrollFrame>
    )
  }

  const data = start.data
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const errors: typeof shown = {}
    if ([...password].length < MIN_PASSWORD) errors.password = `Use at least ${MIN_PASSWORD} characters.`
    if (confirm !== password) errors.confirm = "The passwords do not match."
    if (!/^[0-9]{6}$/.test(code)) errors.form = "Enter the 6-digit code from your authenticator app."
    setShown(errors)
    if (Object.keys(errors).length > 0) {
      focusFirstInvalid(event.currentTarget)
      return
    }
    setBusy(true)
    try {
      setSession(await finishEnrollment(token, password, code))
      void navigate("/", { replace: true })
    } catch (err) {
      if (expired(err)) setGone(true)
      else if (err instanceof ApiError && err.fields.some((f) => f.path === "/code")) setShown({ form: "That code does not match. Try the current one." })
      else if (err instanceof ApiError && err.fields.some((f) => f.path === "/password")) setShown({ password: `Use ${MIN_PASSWORD} to 128 characters.` })
      else setShown({ form: errorMessage(err) })
      setBusy(false)
    }
  }

  return (
    <EnrollFrame>
      <p className="text-sm text-muted-foreground">{`You are joining as ${data.username}. Choose a password, then add the code.`}</p>
      <form className="flex flex-col gap-4" noValidate onSubmit={(event) => void submit(event)}>
        <div className="flex flex-col gap-2">
          <Label htmlFor="enroll-password">Password</Label>
          <Input
            id="enroll-password"
            type="password"
            autoComplete="new-password"
            value={password}
            aria-invalid={shown.password !== undefined}
            aria-describedby="enroll-password-hint"
            onChange={(e) => setPassword(e.target.value)}
            autoFocus
          />
          <p id="enroll-password-hint" className={shown.password === undefined ? "text-xs text-muted-foreground" : "text-sm text-destructive-text"}>
            {shown.password ?? `At least ${MIN_PASSWORD} characters.`}
          </p>
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor="enroll-confirm">Confirm password</Label>
          <Input
            id="enroll-confirm"
            type="password"
            autoComplete="new-password"
            value={confirm}
            aria-invalid={shown.confirm !== undefined}
            aria-describedby={shown.confirm !== undefined ? "enroll-confirm-error" : undefined}
            onChange={(e) => setConfirm(e.target.value)}
          />
          {shown.confirm !== undefined && (
            <p id="enroll-confirm-error" className="text-sm text-destructive-text">
              {shown.confirm}
            </p>
          )}
        </div>
        <QrCode value={data.uri} />
        <p className="text-sm">
          Or enter this key: <code className="font-mono break-all">{groupKey(data.secret)}</code>
        </p>
        <div className="flex flex-col gap-2">
          <Label htmlFor="enroll-code">Authentication code</Label>
          <Input
            id="enroll-code"
            inputMode="numeric"
            autoComplete="one-time-code"
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\s/g, "").slice(0, 6))}
          />
        </div>
        {shown.form !== undefined && (
          <Alert variant="destructive" role="alert">
            <AlertDescription>{shown.form}</AlertDescription>
          </Alert>
        )}
        <Button type="submit" loading={busy}>
          Finish setup
        </Button>
      </form>
    </EnrollFrame>
  )
}
