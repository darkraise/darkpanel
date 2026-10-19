import { useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { ApiError, TOKENS_QUERY_KEY, applyPlan, changeError, errorMessage, planAction } from "@/api/client"
import type { TokenInfo } from "@/api/types"

interface RevokeFormProps {
  token: TokenInfo
  busy: boolean
  setBusy: (busy: boolean) => void
  onDone: () => void
}

function RevokeForm({ token, busy, setBusy, onDone }: RevokeFormProps) {
  const client = useQueryClient()
  // A fresh plan per opening: gcTime 0 drops it when the dialog closes.
  const plan = useQuery({
    queryKey: ["plan", "core.token.revoke", token.id],
    queryFn: () => planAction("core.token.revoke", 1, { id: token.id }),
    staleTime: Infinity,
    gcTime: 0,
  })
  const [typed, setTyped] = useState("")
  const [error, setError] = useState<string | null>(null)

  // An expired, unknown or already-applied plan is dead, so resending it
  // fails the same way; a failed change consumed it. Plan again so the next
  // attempt carries a fresh one.
  function retryWithFreshPlan() {
    setTyped("")
    void plan.refetch()
  }

  async function revoke(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (plan.data === undefined) return
    setBusy(true)
    setError(null)
    try {
      const change = await applyPlan(plan.data, plan.data.confirmation === null ? undefined : typed)
      const failure = changeError(change)
      if (failure !== null) {
        setError(failure)
        retryWithFreshPlan()
        return
      }
      void client.invalidateQueries({ queryKey: TOKENS_QUERY_KEY })
      onDone()
    } catch (err) {
      setError(errorMessage(err))
      if (err instanceof ApiError && err.code.startsWith("plan.")) retryWithFreshPlan()
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle>{`Revoke token ${token.name}`}</DialogTitle>
        <DialogDescription>Scripts that use this token stop working at once. This cannot be undone.</DialogDescription>
      </DialogHeader>
      {plan.isPending ? (
        <Spinner aria-label="Preparing the revocation" />
      ) : plan.isError ? (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{errorMessage(plan.error)}</AlertDescription>
        </Alert>
      ) : (
        <form className="flex flex-col gap-4" onSubmit={(event) => void revoke(event)}>
          {plan.data.confirmation !== null && (
            <div className="flex flex-col gap-2">
              <Label htmlFor="revoke-confirmation">
                Type <code className="font-mono">{plan.data.confirmation}</code> to confirm
              </Label>
              <Input
                id="revoke-confirmation"
                className="font-mono"
                autoComplete="off"
                value={typed}
                onChange={(event) => setTyped(event.target.value)}
              />
            </div>
          )}
          {error !== null && (
            <Alert variant="destructive" role="alert">
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          )}
          <DialogFooter>
            <Button
              type="submit"
              variant="destructive"
              loading={busy}
              disabled={plan.isFetching || (plan.data.confirmation !== null && typed !== plan.data.confirmation)}
            >
              Revoke
            </Button>
          </DialogFooter>
        </form>
      )}
    </>
  )
}

export function RevokeTokenDialog({ token, onClose }: { token: TokenInfo | null; onClose: () => void }) {
  const [busy, setBusy] = useState(false)

  // While a revocation is in flight the dialog cannot be closed, so its
  // completion can never close the dialog of a token opened afterwards.
  return (
    <Dialog
      open={token !== null}
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent>
        {token !== null && <RevokeForm key={token.id} token={token} busy={busy} setBusy={setBusy} onDone={onClose} />}
      </DialogContent>
    </Dialog>
  )
}
