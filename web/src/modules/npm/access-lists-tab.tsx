import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Textarea } from "darkraise-ui/components/textarea"
import { MarkBadges, ReadFailure } from "./list-parts"
import { readAccessListList } from "./list-replies"
import { NpmChange } from "./npm-change"
import { parseRules } from "./parse"
import { npmKey, type AccessClientInput, type AccessListView } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

const RULES_HELP = "One rule per line: allow or deny, then an IP address or CIDR."

function AccessListForm({ onPlan }: { onPlan: (satisfyAny: boolean, clients: AccessClientInput[]) => void }) {
  const [satisfyAny, setSatisfyAny] = useState(false)
  const [text, setText] = useState("")
  const rules = parseRules(text)
  const refusal = !rules.ok && text.trim() !== "" ? rules.error : null

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (rules.ok) onPlan(satisfyAny, rules.clients)
  }

  return (
    <form aria-label="Access list" className="flex flex-col gap-4" onSubmit={submit}>
      <div className="flex items-center gap-2">
        <Checkbox id="accesslist-any" checked={satisfyAny} onCheckedChange={(c) => setSatisfyAny(c === true)} />
        <Label htmlFor="accesslist-any">Satisfy any rule</Label>
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="accesslist-rules">Rules</Label>
        <Textarea
          id="accesslist-rules"
          value={text}
          rows={5}
          autoComplete="off"
          aria-invalid={refusal !== null ? true : undefined}
          aria-describedby="accesslist-rules-help"
          onChange={(e) => setText(e.target.value)}
        />
        <p id="accesslist-rules-help" className="text-sm text-muted-foreground">
          {refusal ?? RULES_HELP}
        </p>
      </div>
      <DialogFooter>
        <Button type="submit" disabled={!rules.ok}>Plan</Button>
      </DialogFooter>
    </form>
  )
}

function AccessListTable({ items, onRemove }: { items: AccessListView[]; onRemove: (a: AccessListView) => void }) {
  if (items.length === 0) return <EmptyState title="No access lists" />
  return (
    <Table aria-label="Access lists">
      <TableHeader>
        <TableRow>
          <TableHead>ID</TableHead>
          <TableHead>Name</TableHead>
          <TableHead>Satisfy</TableHead>
          <TableHead>Rules</TableHead>
          <TableHead>Status</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((a) => (
          <TableRow key={a.id}>
            <TableCell>{a.id}</TableCell>
            <TableCell>{a.name}</TableCell>
            <TableCell>{a.satisfyAny ? "any rule" : "all rules"}</TableCell>
            <TableCell className="font-mono">{a.clients.map((c) => `${c.directive} ${c.address}`).join(", ")}</TableCell>
            <TableCell>
              <MarkBadges reserved={a.reserved} unclaimed={a.unclaimed} />
            </TableCell>
            <TableCell>
              <Button size="sm" variant="outline" aria-label={`Remove ${a.name}`} disabled={a.reserved !== ""} onClick={() => onRemove(a)}>
                Remove
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// AccessListsTab lists the host's NPM access lists and adds or removes one
// through the change dialog.
export function AccessListsTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const [open, setOpen] = useState(false)
  const query = useQuery({
    queryKey: npmKey(hostId, "accesslist.list"),
    queryFn: () => readAccessListList(hostId),
  })

  let content
  if (query.data === undefined) {
    content = query.isPending ? <Spinner aria-label="Loading access lists" /> : <ReadFailure error={query.error} />
  } else {
    content = (
      <>
        {query.isError && <ReadFailure error={query.error} />}
        <AccessListTable
          items={query.data.items}
          onRemove={(a) => setPending({ action: "npm.accesslist.remove", title: `Remove access list ${a.name}`, input: { id: a.id } })}
        />
      </>
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex justify-end">
        <Button onClick={() => setOpen(true)}>Add access list</Button>
      </div>
      {content}
      {open && (
        <Dialog open onOpenChange={setOpen}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>Add an access list</DialogTitle>
              <DialogDescription>The next step shows what the access list changes before anything is applied.</DialogDescription>
            </DialogHeader>
            <AccessListForm
              onPlan={(satisfyAny, clients) => {
                setOpen(false)
                setPending({
                  action: "npm.accesslist.add",
                  title: `Add an access list with ${clients.length} ${clients.length === 1 ? "rule" : "rules"}`,
                  input: { satisfyAny, clients },
                })
              }}
            />
          </DialogContent>
        </Dialog>
      )}
      <NpmChange
        hostId={hostId}
        action={pending?.action ?? "npm.accesslist.add"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </div>
  )
}
