import { createContext, useCallback, useContext, useEffect, useRef, useState, type FormEvent, type ReactNode } from "react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { ApiError, errorMessage, setStepUpHandler, stepUp } from "@/api/client"

export const STEP_UP_UNAVAILABLE = "Set up an authenticator to use the console and jobs"
const DEFAULT_REASON = "This action needs your authenticator code"

// stepUpError is the line a refused code shows under the field (UX-7).
function stepUpError(err: unknown): string {
  if (!(err instanceof ApiError)) return errorMessage(err)
  switch (err.code) {
    case "auth.invalid_credentials":
      return "That code didn't work"
    case "auth.step_up_wait":
      return "Wait for the next code"
    case "auth.rate_limited":
      return err.retryAfterSeconds === null ? "Too many attempts; try again later" : `Too many attempts; try again in ${err.retryAfterSeconds} s`
    default:
      return err.message
  }
}

export interface StepUpDialogProps {
  // The reason line, such as "Opening a terminal needs your authenticator code".
  reason: string
  // Sends the code in place of the step-up endpoint, so a caller that spends
  // the code on its own request (the terminal ticket) never spends it twice.
  // Its errors are mapped as the endpoint's are.
  submit?: (code: string) => Promise<void>
  onVerified: () => void
  onCancel: () => void
}

// StepUpDialog asks for a fresh authenticator code (UI-6, UX-7). It is open
// while mounted, and closing it cancels.
export function StepUpDialog({ reason, submit = stepUp, onVerified, onCancel }: StepUpDialogProps) {
  const field = useRef<HTMLInputElement>(null)
  const [code, setCode] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  const [busy, setBusy] = useState(false)

  async function verify(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await submit(code)
      onVerified()
    } catch (err) {
      if (err instanceof ApiError && err.code === "auth.step_up_unavailable") {
        setUnavailable(true)
        return
      }
      setError(stepUpError(err))
      setCode("")
      field.current?.focus()
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onCancel())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Enter your authenticator code</DialogTitle>
          <DialogDescription>{reason}</DialogDescription>
        </DialogHeader>
        {unavailable ? (
          <>
            <Alert role="status">
              <AlertDescription>{STEP_UP_UNAVAILABLE}</AlertDescription>
            </Alert>
            <DialogFooter>
              <Button variant="outline" onClick={onCancel}>
                Close
              </Button>
            </DialogFooter>
          </>
        ) : (
          <form className="flex flex-col gap-4" onSubmit={(event) => void verify(event)}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="step-up-code">Authenticator code</Label>
              <Input
                ref={field}
                id="step-up-code"
                name="code"
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]{6}"
                maxLength={6}
                required
                autoFocus
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                aria-invalid={error !== null}
                aria-describedby={error !== null ? "step-up-error" : undefined}
              />
              {error !== null && (
                <p id="step-up-error" role="alert" className="text-sm text-destructive-text">
                  {error}
                </p>
              )}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={onCancel}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy || code.length !== 6}>
                Verify
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

export interface StepUp {
  // Opens the dialog and answers true once the code was accepted, false when
  // the user cancelled.
  stepUp: (reason?: string, submit?: (code: string) => Promise<void>) => Promise<boolean>
}

interface Ask {
  id: number
  reason: string
  submit?: (code: string) => Promise<void>
  settle: (ok: boolean) => void
  done: Promise<boolean>
  // The element focused when the step-up was asked for, which gets the focus
  // back: the dialog unmounts on close, so the kit cannot restore it.
  returnTo: Element | null
}

const StepUpContext = createContext<StepUp | null>(null)

// StepUpProvider holds the one step-up dialog of the app and is the API
// client's step-up handler. Asks queue; requests refused at the same time
// share one plain step-up.
export function StepUpProvider({ children }: { children: ReactNode }) {
  const [asks, setAsks] = useState<Ask[]>([])
  const queue = useRef<Ask[]>([])
  const nextId = useRef(0)

  const ask = useCallback((reason = DEFAULT_REASON, submit?: (code: string) => Promise<void>): Promise<boolean> => {
    const shared = submit === undefined ? queue.current.find((a) => a.submit === undefined) : undefined
    if (shared !== undefined) return shared.done
    let settle: (ok: boolean) => void = () => {}
    const done = new Promise<boolean>((resolve) => {
      settle = resolve
    })
    const next: Ask = { id: nextId.current++, reason, submit, settle, done, returnTo: document.activeElement }
    queue.current = [...queue.current, next]
    setAsks(queue.current)
    return done
  }, [])

  useEffect(() => {
    setStepUpHandler(() => ask())
    return () => setStepUpHandler(null)
  }, [ask])

  function finish(current: Ask, ok: boolean) {
    queue.current = queue.current.filter((a) => a !== current)
    setAsks(queue.current)
    current.settle(ok)
    const back = current.returnTo
    if (queue.current.length === 0 && back instanceof HTMLElement) {
      setTimeout(() => {
        if (back.isConnected) back.focus()
      }, 0)
    }
  }

  const current = asks[0]
  return (
    <StepUpContext value={{ stepUp: ask }}>
      {children}
      {current !== undefined && (
        <StepUpDialog
          key={current.id}
          reason={current.reason}
          submit={current.submit}
          onVerified={() => finish(current, true)}
          onCancel={() => finish(current, false)}
        />
      )}
    </StepUpContext>
  )
}

// useStepUp opens the shared step-up dialog.
// eslint-disable-next-line react-refresh/only-export-components -- the contract keeps the hook beside its dialog
export function useStepUp(): StepUp {
  const value = useContext(StepUpContext)
  if (value === null) throw new Error("useStepUp needs a StepUpProvider")
  return value
}
