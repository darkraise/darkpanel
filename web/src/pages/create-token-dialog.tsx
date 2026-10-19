import { useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Clipboard, ClipboardControl, ClipboardInput, ClipboardLabel, ClipboardTrigger } from "darkraise-ui/components/clipboard"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { TOKENS_QUERY_KEY, applyPlan, changeError, errorMessage, planAction } from "@/api/client"
import type { NewToken, Scope } from "@/api/types"

const SCOPES: Array<{ value: Scope; label: string; description: string }> = [
  { value: "read", label: "Read", description: "Reads hosts, resources, changes and the audit log." },
  { value: "operate", label: "Operate", description: "Also plans and applies module changes on hosts." },
  { value: "admin", label: "Admin", description: "Also manages tokens and module enablement." },
]

export function CreateTokenDialog() {
  const client = useQueryClient()
  const [open, setOpen] = useState(false)
  const [scope, setScope] = useState<Scope>("read")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [created, setCreated] = useState<NewToken | null>(null)

  // Closing forgets the token, so it is shown exactly once. While a creation
  // is in flight the dialog cannot be closed, so its token can neither be
  // lost nor land in a later opening.
  function onOpenChange(next: boolean) {
    if (!next && busy) return
    setOpen(next)
    if (!next) {
      setScope("read")
      setError(null)
      setCreated(null)
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy) return
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError(null)
    try {
      const plan = await planAction("core.token.create", 1, {
        name: String(form.get("name") ?? "").trim(),
        scope,
        expiresInDays: Number(form.get("expiresInDays")),
      })
      const change = await applyPlan<NewToken>(plan)
      const failure = changeError(change)
      if (failure !== null || change.output === null) {
        setError(failure ?? "The controller returned no token.")
        return
      }
      setCreated(change.output)
      void client.invalidateQueries({ queryKey: TOKENS_QUERY_KEY })
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger asChild>
        <Button>Create token</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{created === null ? "Create an API token" : "Token created"}</DialogTitle>
          <DialogDescription>
            {created === null
              ? "The CLI and scripts authenticate with a token instead of a password."
              : "Copy the token now. It will not be shown again."}
          </DialogDescription>
        </DialogHeader>
        {created === null ? (
          <form className="flex flex-col gap-4" onSubmit={(event) => void submit(event)}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="token-name">Name</Label>
              <Input id="token-name" name="name" required maxLength={64} autoComplete="off" />
            </div>
            <fieldset className="flex flex-col gap-2">
              <legend className="text-sm font-medium">Scope</legend>
              <RadioGroup value={scope} onValueChange={(value) => setScope(value as Scope)} aria-label="Scope">
                {SCOPES.map((s) => (
                  <div key={s.value} className="flex items-start gap-2">
                    <RadioGroupItem id={`token-scope-${s.value}`} value={s.value} aria-describedby={`token-scope-${s.value}-help`} />
                    <div className="flex flex-col">
                      <Label htmlFor={`token-scope-${s.value}`}>{s.label}</Label>
                      <span id={`token-scope-${s.value}-help`} className="text-xs text-muted-foreground">
                        {s.description}
                      </span>
                    </div>
                  </div>
                ))}
              </RadioGroup>
            </fieldset>
            <div className="flex flex-col gap-2">
              <Label htmlFor="token-expiry">Expires after (days)</Label>
              <Input id="token-expiry" name="expiresInDays" type="number" min={1} max={365} defaultValue={90} required />
            </div>
            {error !== null && (
              <Alert variant="destructive" role="alert">
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}
            <DialogFooter>
              <Button type="submit" loading={busy}>
                Create
              </Button>
            </DialogFooter>
          </form>
        ) : (
          <div className="flex flex-col gap-4">
            <Clipboard value={created.token}>
              <ClipboardLabel>Token {created.name}</ClipboardLabel>
              <ClipboardControl>
                <ClipboardInput className="font-mono" />
                <ClipboardTrigger>Copy</ClipboardTrigger>
              </ClipboardControl>
            </Clipboard>
            <DialogFooter>
              <Button onClick={() => onOpenChange(false)}>Done</Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
