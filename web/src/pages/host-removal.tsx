import { useEffect, useRef, useState, type FormEvent } from "react"
import { useNavigate } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { HOSTS_QUERY_KEY, changeError, changeInProgress } from "@/api/client"
import type { Change, EnrollmentGrant, Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { isGrant } from "@/lib/grant"
import { GrantDialog } from "./grant-dialog"

type Phase = "closed" | "revoke" | "delete" | "token-form" | "token-plan" | "grant"

const ADDRESS = /^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+):[0-9]{1,5}$/

function applied(change: Change): boolean {
  return changeError(change) === null && !changeInProgress(change)
}

// HostRemoval holds the Overview's enrollment and removal controls. Only the
// buttons depend on the host's state: the dialogs stay mounted, so the host
// list refresh that follows an apply cannot unmount an open dialog.
export function HostRemoval({ host }: { host: Host }) {
  const client = useQueryClient()
  const navigate = useNavigate()
  const [phase, setPhase] = useState<Phase>("closed")
  const [address, setAddress] = useState("")
  const [addressError, setAddressError] = useState<string | null>(null)
  const [tokenInput, setTokenInput] = useState<Record<string, unknown>>({})
  const [grant, setGrant] = useState<EnrollmentGrant | null>(null)
  const [grantMissing, setGrantMissing] = useState(false)
  const addressRef = useRef<HTMLInputElement>(null)
  const opener = useRef<HTMLButtonElement | null>(null)

  const removable = !host.isLocal && (host.state === "pending" || host.state === "revoked")
  const revocable = !host.isLocal && host.state !== "revoked"

  // A flow of several dialogs ends in a dialog other than the one its button
  // opened, whose own focus return lands on an element already gone.
  useEffect(() => {
    if (phase !== "closed" || opener.current === null) return
    if (opener.current.isConnected) opener.current.focus()
    opener.current = null
  }, [phase])

  function open(next: Phase, button: HTMLButtonElement) {
    opener.current = button
    setPhase(next)
  }

  function reset() {
    setPhase("closed")
    setAddress("")
    setAddressError(null)
    setTokenInput({})
    setGrant(null)
    setGrantMissing(false)
  }

  function submitToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const typed = address.trim()
    if (typed !== "" && !ADDRESS.test(typed)) {
      setAddressError("Enter host:port, such as panel.example.com:7443.")
      addressRef.current?.focus()
      return
    }
    setAddressError(null)
    setTokenInput(typed === "" ? { hostId: host.id } : { hostId: host.id, controllerAddress: typed })
    setPhase("token-plan")
  }

  function tokenApplied(change: Change) {
    if (!applied(change)) return
    if (isGrant(change.output)) setGrant(change.output)
    else setGrantMissing(true)
    setPhase("grant")
    void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
  }

  return (
    <>
      {removable && <Button variant="outline" onClick={(e) => open("token-form", e.currentTarget)}>New enrollment token</Button>}
      {revocable && <Button variant="destructive" onClick={(e) => open("revoke", e.currentTarget)}>Revoke host</Button>}
      {removable && <Button variant="destructive" onClick={(e) => open("delete", e.currentTarget)}>Delete host</Button>}
      <ChangeDialog
        open={phase === "revoke"}
        onOpenChange={(next) => (next ? undefined : reset())}
        title="Revoke host"
        target={{ name: "core.host.revoke", version: 1 }}
        input={{ hostId: host.id }}
        onApplied={(change) => {
          if (applied(change)) void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
        }}
      />
      <ChangeDialog
        open={phase === "delete"}
        onOpenChange={(next) => (next ? undefined : reset())}
        title="Delete host"
        target={{ name: "core.host.delete", version: 1 }}
        input={{ hostId: host.id }}
        onApplied={(change) => {
          if (!applied(change)) return
          void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
          reset()
          void navigate("/")
        }}
      />
      <Dialog open={phase === "token-form"} onOpenChange={(next) => (next ? undefined : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New enrollment token</DialogTitle>
            <DialogDescription>{`${host.name} gets a one-hour enrollment token for its agent.`}</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" noValidate onSubmit={submitToken}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="token-controller-address">Controller address (optional)</Label>
              <Input
                ref={addressRef}
                id="token-controller-address"
                name="controllerAddress"
                placeholder="controller.example:7443"
                autoComplete="off"
                autoFocus
                value={address}
                onChange={(e) => setAddress(e.target.value)}
                aria-invalid={addressError !== null}
                aria-describedby={addressError !== null ? "token-controller-address-error" : undefined}
              />
              {addressError !== null && (
                <p id="token-controller-address-error" className="text-sm text-destructive">
                  {addressError}
                </p>
              )}
            </div>
            <DialogFooter>
              <Button type="submit">Continue</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ChangeDialog
        open={phase === "token-plan"}
        onOpenChange={(next) => (next ? undefined : reset())}
        title="New enrollment token"
        target={{ name: "core.host.token", version: 1 }}
        input={tokenInput}
        onApplied={tokenApplied}
      />
      <GrantDialog
        grant={phase === "grant" ? grant : null}
        missing={phase === "grant" && grantMissing}
        title="New enrollment token"
        missingMessage="The controller did not return the enrollment token."
        onClose={reset}
      />
    </>
  )
}
