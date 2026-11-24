import { useMemo, useState, type FormEvent, type ReactNode } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Boxes, MoreHorizontal, Search } from "lucide-react"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { GROUPS_QUERY_KEY, HOSTS_QUERY_KEY, USERS_QUERY_KEY, errorMessage, listGroups, listHosts } from "@/api/client"
import type { HostGroup } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { usePageTitle } from "@/lib/page-title"
import { STACKED_BODY, STACKED_CELL, STACKED_FRAME, STACKED_HEAD, STACKED_ROW, STACKED_TABLE } from "@/lib/table"
import { applied } from "@/lib/users"

const GROUP_NAME = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,47}$/

type Dialogue =
  | { kind: "create" }
  | { kind: "rename"; group: HostGroup }
  | { kind: "members"; group: HostGroup }
  | { kind: "change"; title: string; name: string; input: Record<string, unknown> }

function FormDialog({ title, description, submit, onCancel, onSubmit, children }: { title: string; description: ReactNode; submit: string; onCancel: () => void; onSubmit: () => void; children: ReactNode }) {
  function handle(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    onSubmit()
  }
  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onCancel())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" noValidate onSubmit={handle}>
          {children}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit">{submit}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function NameForm({ title, initial, submit, onCancel, onPlan }: { title: string; initial: string; submit: string; onCancel: () => void; onPlan: (name: string) => void }) {
  const [name, setName] = useState(initial)
  const [problem, setProblem] = useState<string | null>(null)
  return (
    <FormDialog
      title={title}
      description="Grants and notification routes name a group, so its hosts share them."
      submit={submit}
      onCancel={onCancel}
      onSubmit={() => {
        if (!GROUP_NAME.test(name.trim())) {
          setProblem("A name is 1 to 48 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit.")
          return
        }
        onPlan(name.trim())
      }}
    >
      <div className="flex flex-col gap-2">
        <Label htmlFor="group-name">Name</Label>
        <Input
          id="group-name"
          value={name}
          autoComplete="off"
          maxLength={48}
          aria-invalid={problem !== null}
          aria-describedby={problem !== null ? "group-name-error" : undefined}
          onChange={(e) => setName(e.target.value)}
        />
        {problem !== null && (
          <p id="group-name-error" className="text-sm text-destructive-text">
            {problem}
          </p>
        )}
      </div>
    </FormDialog>
  )
}

// MembersForm edits a group's hosts: a searchable checklist of every host.
function MembersForm({ group, onCancel, onPlan }: { group: HostGroup; onCancel: () => void; onPlan: (hostIds: string[]) => void }) {
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const [chosen, setChosen] = useState<Set<string>>(() => new Set(group.hostIds))
  const [search, setSearch] = useState("")
  const all = useMemo(() => [...(hosts.data?.hosts ?? [])].sort((a, b) => a.name.localeCompare(b.name)), [hosts.data])
  const needle = search.trim().toLowerCase()
  const shown = needle === "" ? all : all.filter((h) => h.name.toLowerCase().includes(needle))

  function toggle(id: string, on: boolean) {
    const next = new Set(chosen)
    if (on) next.add(id)
    else next.delete(id)
    setChosen(next)
  }

  let list
  if (hosts.isPending) list = <p className="text-sm text-muted-foreground">Loading the hosts…</p>
  else if (hosts.isError)
    list = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't load hosts: ${errorMessage(hosts.error)}`}</span>
          <Button type="button" size="sm" variant="outline" onClick={() => void hosts.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  else if (all.length === 0) list = <p className="text-sm text-muted-foreground">No hosts yet. Add a host first.</p>
  else
    list = (
      <>
        <div className="relative">
          <Search aria-hidden className="absolute top-1/2 left-2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input type="search" aria-label="Search hosts" placeholder="Search hosts" className="pl-8" value={search} onChange={(e) => setSearch(e.target.value)} />
        </div>
        {shown.length === 0 ? (
          <p className="text-sm text-muted-foreground">{`No hosts match "${search.trim()}".`}</p>
        ) : (
          <ul className="flex max-h-72 flex-col gap-2 overflow-y-auto text-sm" aria-label="Hosts">
            {shown.map((h) => (
              <li key={h.id} className="flex min-w-0 items-center gap-2">
                <Checkbox id={`member-${h.id}`} checked={chosen.has(h.id)} onCheckedChange={(on) => toggle(h.id, on === true)} />
                <Label htmlFor={`member-${h.id}`} className="min-w-0 font-normal wrap-anywhere">
                  {h.name}
                </Label>
              </li>
            ))}
          </ul>
        )}
      </>
    )

  return (
    <FormDialog
      title={`Members of ${group.name}`}
      description={hosts.data === undefined ? "Pick the hosts this group holds." : `${chosen.size} of ${all.length} hosts are members.`}
      submit="Review members"
      onCancel={onCancel}
      onSubmit={() => {
        if (hosts.data !== undefined) onPlan([...chosen].sort())
      }}
    >
      {list}
    </FormDialog>
  )
}

function RowMenu({ group, onPick }: { group: HostGroup; onPick: (d: Dialogue) => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={`Actions for ${group.name}`}>
          <MoreHorizontal aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => onPick({ kind: "members", group })}>Edit members</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onPick({ kind: "rename", group })}>Rename</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onPick({ kind: "change", title: `Delete ${group.name}`, name: "core.group.delete", input: { id: group.id } })}>
          Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function GroupsTable({ groups, onPick }: { groups: HostGroup[]; onPick: (d: Dialogue) => void }) {
  return (
    <div className={STACKED_FRAME}>
      <Table className={STACKED_TABLE}>
        <TableHeader className={STACKED_HEAD}>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead className="text-right">Hosts</TableHead>
            <TableHead className="text-right">Grants</TableHead>
            <TableHead className="w-10">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className={STACKED_BODY}>
          {groups.map((g) => (
            <TableRow className={STACKED_ROW} key={g.id}>
              <TableCell data-label="Name" className={`max-w-96 font-medium whitespace-normal wrap-anywhere ${STACKED_CELL}`}>{g.name}</TableCell>
              <TableCell data-label="Hosts" className={`text-right tabular-nums ${STACKED_CELL}`}>{g.hostCount}</TableCell>
              <TableCell data-label="Grants" className={`text-right tabular-nums ${STACKED_CELL}`}>{g.grantCount}</TableCell>
              <TableCell data-label="Actions" className={STACKED_CELL}>
                <RowMenu group={g} onPick={onPick} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

export function GroupsPage() {
  usePageTitle("Host groups")
  const client = useQueryClient()
  const groups = useQuery({ queryKey: GROUPS_QUERY_KEY, queryFn: listGroups })
  const [dialogue, setDialogue] = useState<Dialogue | null>(null)
  const close = () => setDialogue(null)
  const create = <Button onClick={() => setDialogue({ kind: "create" })}>Create group</Button>

  let body
  if (groups.isPending) {
    body = (
      <div className="flex flex-col gap-2" aria-label="Loading the host groups">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-2/3" />
      </div>
    )
  } else if (groups.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't load host groups: ${errorMessage(groups.error)}`}</span>
          <Button size="sm" variant="outline" onClick={() => void groups.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  } else if (groups.data.groups.length === 0) {
    // darkraise-ui's EmptyState title is an h3; the hidden h2 keeps the
    // heading levels from skipping one.
    body = (
      <>
        <h2 className="sr-only">Host group list</h2>
        <EmptyState icon={<Boxes aria-hidden />} title="No host groups yet" description="Group hosts to grant access to them together." />
      </>
    )
  } else {
    body = <GroupsTable groups={groups.data.groups} onPick={setDialogue} />
  }

  return (
    <>
      <PageHeader title="Host groups" description="Sets of hosts that member grants and notification routes name." actions={create} />
      {body}
      {dialogue?.kind === "create" && (
        <NameForm
          title="Create group"
          initial=""
          submit="Review group"
          onCancel={close}
          onPlan={(name) => setDialogue({ kind: "change", title: `Create the group ${name}`, name: "core.group.create", input: { name } })}
        />
      )}
      {dialogue?.kind === "rename" && (
        <NameForm
          title={`Rename ${dialogue.group.name}`}
          initial={dialogue.group.name}
          submit="Review change"
          onCancel={close}
          onPlan={(name) => setDialogue({ kind: "change", title: `Rename ${dialogue.group.name}`, name: "core.group.rename", input: { id: dialogue.group.id, name } })}
        />
      )}
      {dialogue?.kind === "members" && (
        <MembersForm
          group={dialogue.group}
          onCancel={close}
          onPlan={(hostIds) =>
            setDialogue({ kind: "change", title: `Change the members of ${dialogue.group.name}`, name: "core.group.set-members", input: { id: dialogue.group.id, hostIds } })
          }
        />
      )}
      {dialogue?.kind === "change" && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : close())}
          title={dialogue.title}
          target={{ name: dialogue.name, version: 1 }}
          input={dialogue.input}
          onApplied={(change) => {
            if (!applied(change)) return
            void client.invalidateQueries({ queryKey: GROUPS_QUERY_KEY })
            void client.invalidateQueries({ queryKey: USERS_QUERY_KEY })
          }}
        />
      )}
    </>
  )
}
