import { useEffect, useRef, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { MoreHorizontal } from "lucide-react"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { SegmentGroup, SegmentGroupIndicator, SegmentGroupItem } from "darkraise-ui/components/segment-group"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { GROUPS_QUERY_KEY, USERS_QUERY_KEY, errorMessage, listGroups, listUsers } from "@/api/client"
import type { DirectoryUser, Grant, HostGroup, Invitation, Role } from "@/api/types"
import { ChangeDialog, type ChangeTarget } from "@/components/change-dialog"
import { usePageTitle } from "@/lib/page-title"
import { STACKED_BODY, STACKED_CELL, STACKED_FRAME, STACKED_HEAD, STACKED_ROW, STACKED_TABLE } from "@/lib/table"
import { accessText, applied, draftOf, grantsOf, invitationOf, methodsText, type AccessDraft } from "@/lib/users"
import { AccessFields, InviteUser, LinkDialog } from "./invite-dialog"

const dayFormat = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" })

function stateText(user: DirectoryUser): string {
  if (user.disabled) return "Disabled"
  if (user.state !== "pending") return "Active"
  if (user.inviteExpiresAt === null) return "Invited"
  const expires = new Date(user.inviteExpiresAt)
  return expires.getTime() <= Date.now() ? "Invited, link expired" : `Invited, expires ${dayFormat.format(expires)}`
}

type Dialogue =
  | { kind: "role"; user: DirectoryUser }
  | { kind: "access"; user: DirectoryUser }
  | { kind: "identity"; user: DirectoryUser }
  | { kind: "change"; user: DirectoryUser; title: string; target: ChangeTarget; input: Record<string, unknown>; link?: boolean }

function RowMenu({ user, onPick }: { user: DirectoryUser; onPick: (d: Dialogue) => void }) {
  const simple = (title: string, name: string, link = false): Dialogue => ({
    kind: "change",
    user,
    title,
    target: { name, version: 1 },
    input: { username: user.username },
    link,
  })
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={`Actions for ${user.username}`}>
          <MoreHorizontal aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => onPick({ kind: "role", user })}>Change role</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onPick({ kind: "access", user })}>Edit access</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onPick({ kind: "identity", user })}>Edit sign-in links</DropdownMenuItem>
        {user.state === "pending" && (
          <>
            <DropdownMenuItem onSelect={() => onPick(simple(`New enrollment link for ${user.username}`, "core.user.renew-invite", true))}>
              New enrollment link
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onPick(simple(`Cancel the invite of ${user.username}`, "core.user.cancel-invite"))}>Cancel invite</DropdownMenuItem>
          </>
        )}
        {user.disabled ? (
          <DropdownMenuItem onSelect={() => onPick(simple(`Enable ${user.username}`, "core.user.enable"))}>Enable</DropdownMenuItem>
        ) : (
          user.state !== "pending" && <DropdownMenuItem onSelect={() => onPick(simple(`Disable ${user.username}`, "core.user.disable"))}>Disable</DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// FormDialog collects an edit's input, then hands it to the change dialog.
function FormDialog({ title, description, onCancel, onSubmit, children }: { title: string; description: string; onCancel: () => void; onSubmit: () => void; children: React.ReactNode }) {
  function submit(event: FormEvent<HTMLFormElement>) {
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
        <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
          {children}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit">Review change</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RoleForm({ user, onCancel, onPlan }: { user: DirectoryUser; onCancel: () => void; onPlan: (role: Role) => void }) {
  const [role, setRole] = useState<Role>(user.role)
  return (
    <FormDialog title={`Change the role of ${user.username}`} description="An admin can do everything; a member only what its access grants." onCancel={onCancel} onSubmit={() => onPlan(role)}>
      <SegmentGroup value={role} onValueChange={(v) => setRole(v as Role)} aria-label="Role" variant="outline">
        <SegmentGroupIndicator />
        <SegmentGroupItem value="member">Member</SegmentGroupItem>
        <SegmentGroupItem value="admin">Admin</SegmentGroupItem>
      </SegmentGroup>
    </FormDialog>
  )
}

function AccessForm({ user, onCancel, onPlan }: { user: DirectoryUser; onCancel: () => void; onPlan: (grants: Grant[]) => void }) {
  const [draft, setDraft] = useState<AccessDraft>(() => draftOf(user.grants))
  return (
    <FormDialog
      title={`Edit the access of ${user.username}`}
      description={user.role === "admin" ? "This user is an admin: its access applies once it is a member." : "Pick the hosts this member may read or operate."}
      onCancel={onCancel}
      onSubmit={() => onPlan(grantsOf(draft))}
    >
      <AccessFields prefix="edit-access" value={draft} onChange={setDraft} />
    </FormDialog>
  )
}

function IdentityForm({ user, onCancel, onPlan }: { user: DirectoryUser; onCancel: () => void; onPlan: (email: string, tailnetLogin: string) => void }) {
  const [email, setEmail] = useState(user.email)
  const [tailnet, setTailnet] = useState(user.tailnetLogin)
  return (
    <FormDialog
      title={`Edit the sign-in links of ${user.username}`}
      description="An OIDC or Tailscale sign-in reaches this user when its address or login matches."
      onCancel={onCancel}
      onSubmit={() => onPlan(email.trim(), tailnet.trim())}
    >
      <div className="flex flex-col gap-2">
        <Label htmlFor="identity-email">Email</Label>
        <Input id="identity-email" type="email" value={email} autoComplete="off" onChange={(e) => setEmail(e.target.value)} />
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="identity-tailnet">Tailnet login</Label>
        <Input id="identity-tailnet" value={tailnet} autoComplete="off" onChange={(e) => setTailnet(e.target.value)} />
      </div>
    </FormDialog>
  )
}

function UsersTable({ users, groups, onPick }: { users: DirectoryUser[]; groups: HostGroup[] | undefined; onPick: (d: Dialogue) => void }) {
  return (
    <div className={STACKED_FRAME}>
      <Table className={STACKED_TABLE}>
        <TableHeader className={STACKED_HEAD}>
          <TableRow>
            <TableHead>Username</TableHead>
            <TableHead>Role</TableHead>
            <TableHead>Access</TableHead>
            <TableHead>Sign-in methods</TableHead>
            <TableHead>State</TableHead>
            <TableHead className="w-10">
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className={STACKED_BODY}>
          {users.map((u) => {
            const access = accessText(u, groups)
            return (
              <TableRow className={STACKED_ROW} key={u.id} data-username={u.username} tabIndex={-1}>
                <TableCell data-label="Username" className={`max-w-48 font-medium whitespace-normal wrap-anywhere ${STACKED_CELL}`}>{u.username}</TableCell>
                <TableCell data-label="Role" className={STACKED_CELL}>
                  <Badge variant={u.role === "admin" ? "default" : "secondary"}>{u.role === "admin" ? "Admin" : "Member"}</Badge>
                </TableCell>
                <TableCell data-label="Access" className={`max-w-72 truncate ${STACKED_CELL}`} title={access}>
                  {access}
                </TableCell>
                <TableCell data-label="Sign-in methods" className={`whitespace-normal ${STACKED_CELL}`}>{methodsText(u.signIn)}</TableCell>
                <TableCell data-label="State" className={STACKED_CELL}>{stateText(u)}</TableCell>
                <TableCell data-label="Actions" className={STACKED_CELL}>
                  <RowMenu user={u} onPick={onPick} />
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
    </div>
  )
}

function focusRow(username: string): boolean {
  const row = document.querySelector<HTMLElement>(`tr[data-username="${CSS.escape(username)}"]`)
  row?.focus()
  return row !== null
}

export function UsersPage() {
  usePageTitle("Users")
  const client = useQueryClient()
  const users = useQuery({ queryKey: USERS_QUERY_KEY, queryFn: listUsers })
  const groups = useQuery({ queryKey: GROUPS_QUERY_KEY, queryFn: listGroups })
  const [dialogue, setDialogue] = useState<Dialogue | null>(null)
  const [link, setLink] = useState<{ username: string; invitation: Invitation } | null>(null)
  const focusUser = useRef<string | null>(null)
  const [focusTick, setFocusTick] = useState(0)

  // The row of an invited or renewed user takes focus once the list holds
  // it: the dialogs that led there all closed, and their openers are gone.
  useEffect(() => {
    if (focusUser.current !== null && focusRow(focusUser.current)) focusUser.current = null
  }, [focusTick, users.data])

  function focusLater(username: string | null) {
    focusUser.current = username
    setFocusTick((n) => n + 1)
  }

  function plan(user: DirectoryUser, title: string, name: string, input: Record<string, unknown>) {
    setDialogue({ kind: "change", user, title, target: { name, version: 1 }, input: { username: user.username, ...input } })
  }

  let body
  if (users.isPending) {
    body = (
      <div className="flex flex-col gap-2" aria-label="Loading the users">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-2/3" />
      </div>
    )
  } else if (users.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't load users: ${errorMessage(users.error)}`}</span>
          <Button size="sm" variant="outline" onClick={() => void users.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  } else {
    body = <UsersTable users={users.data.users} groups={groups.data?.groups} onPick={setDialogue} />
  }

  const close = () => setDialogue(null)
  return (
    <>
      <PageHeader title="Users" description="People who sign in to this panel, and what they may do." actions={<InviteUser onDone={focusLater} />} />
      {body}
      {dialogue?.kind === "role" && (
        <RoleForm user={dialogue.user} onCancel={close} onPlan={(role) => plan(dialogue.user, `Change the role of ${dialogue.user.username}`, "core.user.set-role", { role })} />
      )}
      {dialogue?.kind === "access" && (
        <AccessForm user={dialogue.user} onCancel={close} onPlan={(grants) => plan(dialogue.user, `Edit the access of ${dialogue.user.username}`, "core.user.set-grants", { grants })} />
      )}
      {dialogue?.kind === "identity" && (
        <IdentityForm
          user={dialogue.user}
          onCancel={close}
          onPlan={(email, tailnetLogin) => plan(dialogue.user, `Edit the sign-in links of ${dialogue.user.username}`, "core.user.set-identity", { email, tailnetLogin })}
        />
      )}
      {dialogue?.kind === "change" && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : close())}
          title={dialogue.title}
          target={dialogue.target}
          input={dialogue.input}
          onApplied={(change) => {
            if (!applied(change)) return
            void client.invalidateQueries({ queryKey: USERS_QUERY_KEY })
            const inv = dialogue.link === true ? invitationOf(change) : null
            if (inv !== null) {
              setLink({ username: dialogue.user.username, invitation: inv })
              setDialogue(null)
            }
          }}
        />
      )}
      <LinkDialog
        username={link?.username ?? ""}
        invitation={link?.invitation ?? null}
        onClose={() => {
          const name = link?.username ?? null
          setLink(null)
          focusLater(name)
        }}
      />
    </>
  )
}
