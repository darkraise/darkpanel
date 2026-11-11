import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Clipboard, ClipboardControl, ClipboardInput, ClipboardLabel, ClipboardTrigger } from "darkraise-ui/components/clipboard"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import type { EnrollmentGrant } from "@/api/types"
import { formatTime } from "@/lib/format"

// GrantDialog shows an enrollment grant once: its token exists only in the
// apply response, so closing the dialog forgets it.
export function GrantDialog({
  grant,
  missing,
  title,
  missingMessage,
  onClose,
}: {
  grant: EnrollmentGrant | null
  missing: boolean
  title: string
  missingMessage: string
  onClose: () => void
}) {
  const bundle = typeof grant?.bundle === "string" && grant.bundle !== "" ? grant.bundle : null
  return (
    <Dialog open={grant !== null || missing} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>
            {missing
              ? "The host exists, but it cannot enroll with this answer."
              : bundle !== null
                ? "Copy the bootstrap bundle now. It will not be shown again."
                : "Copy the enrollment token now. It will not be shown again."}
          </DialogDescription>
        </DialogHeader>
        {missing && (
          <div className="flex flex-col gap-4">
            <Alert variant="destructive" role="alert">
              <AlertDescription>{missingMessage}</AlertDescription>
            </Alert>
            <DialogFooter>
              <Button onClick={onClose}>Done</Button>
            </DialogFooter>
          </div>
        )}
        {grant !== null && (
          <div className="flex flex-col gap-4">
            <Clipboard value={bundle ?? grant.token}>
              <ClipboardLabel htmlFor="host-grant-secret">{bundle !== null ? `Bootstrap bundle for ${grant.host.name}` : `Enrollment token for ${grant.host.name}`}</ClipboardLabel>
              <ClipboardControl>
                <ClipboardInput id="host-grant-secret" className="font-mono" />
                <ClipboardTrigger>Copy</ClipboardTrigger>
              </ClipboardControl>
            </Clipboard>
            <p className="text-sm text-muted-foreground">
              {bundle !== null
                ? `On the new host, run install.sh --role agent --token-file <file> with this bundle before ${formatTime(grant.expiresAt)}.`
                : `On the new host, run sudo darkpanel agent enroll and paste the token before ${formatTime(grant.expiresAt)}.`}
            </p>
            <DialogFooter>
              <Button onClick={onClose}>Done</Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
