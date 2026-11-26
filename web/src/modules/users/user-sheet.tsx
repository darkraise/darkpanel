import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Trash2 } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Label } from "darkraise-ui/components/label"
import { Sheet, SheetBody, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "darkraise-ui/components/sheet"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Textarea } from "darkraise-ui/components/textarea"
import { errorMessage, readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { applied } from "@/lib/users"
import { keyError, keyType, type Account, type Key } from "./keys"

type KeyChange = { kind: "add"; key: string } | { kind: "remove"; key: Key }

function KeyList({ hostId, name, admin, onRemove }: { hostId: string; name: string; admin: boolean; onRemove: (k: Key) => void }) {
  const keys = useQuery({
    queryKey: ["resource", hostId, "users.key.list", name],
    queryFn: () => readAction<{ keys: Key[] }>("users.key.list", 1, { name }, hostId),
  })
  if (keys.isPending) {
    return (
      <div className="flex flex-col gap-2" role="status" aria-label="Loading the keys">
        <Skeleton className="h-6 w-full" />
        <Skeleton className="h-6 w-2/3" />
      </div>
    )
  }
  if (keys.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't load the keys: ${errorMessage(keys.error)}`}</span>
          <Button size="sm" variant="outline" onClick={() => void keys.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  }
  if (keys.data.keys.length === 0) return <p className="text-sm text-muted-foreground">No authorized keys</p>
  return (
    <div className="overflow-x-auto">
      <Table aria-label="Authorized keys">
        <TableHeader>
          <TableRow>
            <TableHead>Type</TableHead>
            <TableHead>Fingerprint</TableHead>
            <TableHead>Comment</TableHead>
            {admin && (
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {keys.data.keys.map((k) => (
            <TableRow key={k.fingerprint}>
              <TableCell className="whitespace-nowrap">{keyType(k)}</TableCell>
              <TableCell className="max-w-[12rem] font-mono text-xs">
                <span className="block truncate" title={k.fingerprint}>
                  {k.fingerprint}
                </span>
              </TableCell>
              <TableCell className="max-w-[10rem]">
                <span className="block truncate" title={k.comment}>
                  {k.comment}
                </span>
              </TableCell>
              {admin && (
                <TableCell>
                  <Button variant="ghost" size="icon" aria-label={`Remove key ${k.comment === "" ? k.fingerprint : k.comment}`} onClick={() => onRemove(k)}>
                    <Trash2 aria-hidden="true" />
                  </Button>
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

// UserSheet shows an account's authorized keys and, for admins, adds and
// removes them (UI-3, UX-4).
export function UserSheet({ hostId, account, admin, onClose }: { hostId: string; account: Account; admin: boolean; onClose: () => void }) {
  const client = useQueryClient()
  const [draft, setDraft] = useState("")
  const [change, setChange] = useState<KeyChange | null>(null)
  const problem = draft.trim() === "" ? null : keyError(draft)

  function refresh() {
    void client.invalidateQueries({ queryKey: ["resource", hostId, "users.key.list", account.name] })
    void client.invalidateQueries({ queryKey: ["resource", hostId, "users.user.list"] })
  }

  return (
    <Sheet open onOpenChange={(open) => (open ? undefined : onClose())}>
      <SheetContent side="end" className="w-full sm:max-w-lg">
        <SheetHeader>
          <SheetTitle className="truncate" title={account.name}>
            {account.name}
          </SheetTitle>
          <SheetDescription>{`UID ${account.uid}, home ${account.home}`}</SheetDescription>
        </SheetHeader>
        <SheetBody className="flex flex-col gap-4">
          <h3 className="text-sm font-semibold">Authorized keys</h3>
          <KeyList hostId={hostId} name={account.name} admin={admin} onRemove={(key) => setChange({ kind: "remove", key })} />
          {admin && (
            <form
              className="flex flex-col gap-2"
              onSubmit={(e) => {
                e.preventDefault()
                if (draft.trim() !== "" && problem === null) setChange({ kind: "add", key: draft.trim() })
              }}
            >
              <Label htmlFor="user-key">Add a public key</Label>
              <Textarea
                id="user-key"
                rows={3}
                value={draft}
                placeholder="ssh-ed25519 AAAA… comment"
                spellCheck={false}
                aria-invalid={problem !== null}
                aria-describedby={problem !== null ? "user-key-error" : undefined}
                onChange={(e) => setDraft(e.target.value)}
                className="font-mono text-xs"
              />
              {problem !== null && (
                <p id="user-key-error" className="text-sm text-destructive-text">
                  {problem}
                </p>
              )}
              <div>
                <Button type="submit" disabled={draft.trim() === "" || problem !== null}>
                  Add key
                </Button>
              </div>
            </form>
          )}
        </SheetBody>
      </SheetContent>
      {change !== null && (
        <ChangeDialog
          open
          onOpenChange={(open) => (open ? undefined : setChange(null))}
          title={change.kind === "add" ? `Add a key for ${account.name}` : `Remove a key from ${account.name}`}
          target={{ name: `users.key.${change.kind}`, version: 1, hostId, module: "users" }}
          input={change.kind === "add" ? { name: account.name, key: change.key } : { name: account.name, fingerprint: change.key.fingerprint }}
          onApplied={(c: Change) => {
            if (!applied(c)) return
            if (change.kind === "add") setDraft("")
            toast(change.kind === "add" ? `Key added for ${account.name}` : `Key removed from ${account.name}`)
            refresh()
          }}
        />
      )}
    </Sheet>
  )
}
