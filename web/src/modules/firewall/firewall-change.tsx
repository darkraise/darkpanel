import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { ApiError } from "@/api/client"
import { ChangeDialog } from "@/components/change-dialog"
import { FIREWALL_ACTION_VERSION, FIREWALL_MODULE } from "./types"

export interface FirewallChangeProps {
  hostId: string
  action: string // a mutating firewall action, such as "firewall.rule.add"
  title: string
  input: Record<string, unknown> | null // null keeps the dialog closed
  onClose: () => void
}

// What the operator has been asked and answered for one input, found by the
// JSON of that input so an answer left from another input is ignored.
interface Consent {
  key: string
  refused: string | null // the server's detail while the question is open
  manual: boolean // the operator agreed to change the manual rule
}

// FirewallChange plans and applies one firewall action through the change
// dialog. Every mutating firewall action is revertible, so the dialog asks
// for the revert timeout. The host's resources are read again once the
// change applies and when the dialog closes, since a revert by the timer
// raises no event that names them. An action that finds a manual rule only
// while planning is refused with firewall.manual_required; the operator is
// asked, and the plan is made again with manual: true once they agree.
export function FirewallChange({ hostId, action, title, input, onClose }: FirewallChangeProps) {
  const client = useQueryClient()
  const [consent, setConsent] = useState<Consent | null>(null)
  const refresh = () => void client.invalidateQueries({ queryKey: ["resource", hostId] })

  const key = JSON.stringify(input ?? {})
  const current = consent !== null && consent.key === key ? consent : null
  const manual = input?.manual === true || current?.manual === true
  const planned = input === null || !manual ? input : { ...input, manual: true }
  const refused = current?.refused ?? null
  const asking = input !== null && !manual

  // ChangeDialog keeps its plan error to itself, so the refusal is read from
  // the query cache.
  useEffect(() => {
    if (!asking) return
    return client.getQueryCache().subscribe((event) => {
      if (event.type !== "updated" || event.action.type !== "error") return
      const k = event.query.queryKey
      if (k[0] !== "plan" || k[1] !== action || k[3] !== hostId || k[4] !== key) return
      const error = event.action.error
      if (error instanceof ApiError && error.code === "firewall.manual_required") {
        setConsent({ key, refused: error.message, manual: false })
      }
    })
  }, [asking, client, action, hostId, key])

  function close() {
    setConsent(null)
    onClose()
    refresh()
  }

  return (
    <>
      <ChangeDialog
        open={planned !== null && refused === null}
        onOpenChange={(open) => {
          if (!open) close()
        }}
        title={title}
        target={{ name: action, version: FIREWALL_ACTION_VERSION, hostId, module: FIREWALL_MODULE, revertible: true }}
        input={planned ?? undefined}
        onApplied={refresh}
      />
      <Dialog open={refused !== null} onOpenChange={(open) => !open && close()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Change a manual rule?</DialogTitle>
            <DialogDescription>{refused}</DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={close}>
              Cancel
            </Button>
            <Button variant="destructive" onClick={() => setConsent({ key, refused: null, manual: true })}>
              Change it anyway
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}
