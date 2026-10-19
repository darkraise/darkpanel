import { useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { TOKENS_QUERY_KEY, applyPlan, changeError, errorMessage, planAction } from "@/api/client"
import type { TokenInfo } from "@/api/types"

function RevokeForm({ token, onDone }: { token: TokenInfo; onDone: () => void }) {
  const client = useQueryClient()
  // A fresh plan per opening: gcTime 0 drops it when the dialog closes.
  const plan = useQuery({
    queryKey: ["plan", "core.token.revoke", token.id],
    queryFn: () => planAction("core.token.revoke", 1, { id: token.id }),
    staleTime: Infinity,
    gcTime: 0,
  })
  const [typed, setTyped] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

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
        return
      }
      void client.invalidateQueries({ queryKey: TOKENS_QUERY_KEY })
      onDone()
    } catch (err) {
      setError(errorMessage(err))
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
              disabled={plan.data.confirmation !== null && typed !== plan.data.confirmation}
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
  return (
    <Dialog
      open={token !== null}
      onOpenChange={(open) => {
        if (!open) onClose()
      }}
    >
      <DialogContent>{token !== null && <RevokeForm key={token.id} token={token} onDone={onClose} />}</DialogContent>
    </Dialog>
  )
}
