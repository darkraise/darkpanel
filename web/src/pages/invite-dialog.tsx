import { useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { ClipboardControl, ClipboardInput, ClipboardLabel, ClipboardTrigger } from "darkraise-ui/components/clipboard"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { SegmentGroup, SegmentGroupItem } from "darkraise-ui/components/segment-group"
import { GROUPS_QUERY_KEY, USERS_QUERY_KEY, errorMessage, listGroups } from "@/api/client"
import type { GrantRole, HostGroup, Invitation, Role } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { SecretClipboard } from "@/components/secret-clipboard"
import { formatTime } from "@/lib/format"
import { applied, enrollLink, grantsOf, invitationOf, type AccessDraft } from "@/lib/users"

const USERNAME = /^[a-z0-9][a-z0-9._-]{0,63}$/

function RolePick({ label, value, disabled, onChange }: { label: string; value: GrantRole; disabled: boolean; onChange: (r: GrantRole) => void }) {
  return (
    <SegmentGroup value={value} onValueChange={(v) => onChange(v as GrantRole)} aria-label={label} variant="outline" disabled={disabled}>
      <SegmentGroupItem value="viewer">Viewer</SegmentGroupItem>
      <SegmentGroupItem value="operator">Operator</SegmentGroupItem>
    </SegmentGroup>
  )
}

function AccessRow({ id, name, role, onChange }: { id: string; name: string; role: GrantRole | undefined; onChange: (r: GrantRole | null) => void }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
      <div className="flex min-w-0 items-center gap-2">
        <Checkbox id={id} checked={role !== undefined} onCheckedChange={(on) => onChange(on === true ? (role ?? "viewer") : null)} />
        <Label htmlFor={id} className="min-w-0 wrap-anywhere">
          {name}
        </Label>
      </div>
      <RolePick label={`Role on ${name}`} value={role ?? "viewer"} disabled={role === undefined} onChange={onChange} />
    </div>
  )
}

// AccessFields edits a member's grants: all hosts or a checklist of groups,
// each with the role it gives.
export function AccessFields({ prefix, value, onChange }: { prefix: string; value: AccessDraft; onChange: (d: AccessDraft) => void }) {
  const groups = useQuery({ queryKey: GROUPS_QUERY_KEY, queryFn: listGroups })
  const setGroup = (g: HostGroup, role: GrantRole | null) => {
    const next = { ...value.groups }
    if (role === null) delete next[g.id]
    else next[g.id] = role
    onChange({ ...value, groups: next })
  }
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-sm font-medium">Access</legend>
      <AccessRow id={`${prefix}-all`} name="All hosts" role={value.all ?? undefined} onChange={(role) => onChange({ ...value, all: role })} />
      {groups.isPending && <p className="text-sm text-muted-foreground">Loading the host groups…</p>}
      {groups.isError && <p className="text-sm text-destructive-text">{`Couldn't load host groups: ${errorMessage(groups.error)}`}</p>}
      <div className="flex max-h-64 flex-col gap-2 overflow-y-auto">
        {groups.data?.groups.map((g) => (
          <AccessRow key={g.id} id={`${prefix}-group-${g.id}`} name={g.name} role={value.groups[g.id]} onChange={(role) => setGroup(g, role)} />
        ))}
      </div>
      {groups.data?.groups.length === 0 && <p className="text-sm text-muted-foreground">No host groups yet: access can only cover all hosts.</p>}
    </fieldset>
  )
}

// LinkDialog shows an enrollment link once: its token exists only in the
// apply answer, so closing the dialog forgets it.
export function LinkDialog({ username, invitation, onClose }: { username: string; invitation: Invitation | null; onClose: () => void }) {
  return (
    <Dialog open={invitation !== null} onOpenChange={(next) => (next ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Enrollment link for ${username}`}</DialogTitle>
          <DialogDescription>Copy this link now. It is shown once.</DialogDescription>
        </DialogHeader>
        {invitation !== null && (
          <div className="flex flex-col gap-4">
            <SecretClipboard value={enrollLink(invitation.token)}>
              <ClipboardLabel htmlFor="enroll-link">Enrollment link</ClipboardLabel>
              <ClipboardControl>
                <ClipboardInput id="enroll-link" className="font-mono" />
                <ClipboardTrigger>Copy</ClipboardTrigger>
              </ClipboardControl>
            </SecretClipboard>
            <p className="text-sm text-muted-foreground">{`Expires ${formatTime(invitation.expiresAt)}`}</p>
            <DialogFooter>
              <Button onClick={onClose}>Done</Button>
            </DialogFooter>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}

type Phase = "closed" | "form" | "plan" | "link"

const EMPTY: AccessDraft = { all: null, groups: {} }

// InviteUser is the Users page's "Invite user" flow (UX-1): the form, the
// change dialog that plans and applies core.user.invite, then the link once.
// onDone gets the invited username when the link dialog closes.
export function InviteUser({ onDone }: { onDone: (username: string) => void }) {
  const client = useQueryClient()
  const [phase, setPhase] = useState<Phase>("closed")
  const [username, setUsername] = useState("")
  const [role, setRole] = useState<Role>("member")
  const [access, setAccess] = useState<AccessDraft>(EMPTY)
  const [email, setEmail] = useState("")
  const [tailnet, setTailnet] = useState("")
  const [problem, setProblem] = useState<string | null>(null)
  const [invitation, setInvitation] = useState<Invitation | null>(null)
  const [missing, setMissing] = useState(false)

  function reset() {
    setPhase("closed")
    setUsername("")
    setRole("member")
    setAccess(EMPTY)
    setEmail("")
    setTailnet("")
    setProblem(null)
    setInvitation(null)
    setMissing(false)
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!USERNAME.test(username.trim())) {
      setProblem("A username is 1 to 64 lowercase letters, digits, dots, dashes or underscores, starting with a letter or digit.")
      return
    }
    setProblem(null)
    setPhase("plan")
  }

  const input = {
    username: username.trim(),
    role,
    grants: role === "member" ? grantsOf(access) : [],
    email: email.trim(),
    tailnetLogin: tailnet.trim(),
  }

  function done() {
    const name = input.username
    reset()
    onDone(name)
  }

  return (
    <>
      <Button onClick={() => setPhase("form")}>Invite user</Button>
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? undefined : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Invite user</DialogTitle>
            <DialogDescription>The user sets a password and a code from a link you send them.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="invite-username">Username</Label>
              <Input
                id="invite-username"
                value={username}
                autoComplete="off"
                autoFocus
                maxLength={64}
                aria-invalid={problem !== null}
                aria-describedby={problem !== null ? "invite-username-error" : undefined}
                onChange={(e) => setUsername(e.target.value)}
              />
              {problem !== null && (
                <p id="invite-username-error" className="text-sm text-destructive-text">
                  {problem}
                </p>
              )}
            </div>
            <div className="flex flex-col gap-1.5">
              <span id="invite-role" className="text-sm font-medium">
                Role
              </span>
              <SegmentGroup value={role} onValueChange={(v) => setRole(v as Role)} aria-labelledby="invite-role" variant="outline">
                <SegmentGroupItem value="member">Member</SegmentGroupItem>
                <SegmentGroupItem value="admin">Admin</SegmentGroupItem>
              </SegmentGroup>
            </div>
            {role === "member" ? (
              <AccessFields prefix="invite-access" value={access} onChange={setAccess} />
            ) : (
              <p className="text-sm text-muted-foreground">An admin can do everything on every host.</p>
            )}
            <div className="flex flex-col gap-2">
              <Label htmlFor="invite-email">Email (optional)</Label>
              <Input id="invite-email" type="email" value={email} autoComplete="off" placeholder="noor@example.com" aria-describedby="invite-email-hint" onChange={(e) => setEmail(e.target.value)} />
              <p id="invite-email-hint" className="text-xs text-muted-foreground">
                Links an OIDC account with this address.
              </p>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="invite-tailnet">Tailnet login (optional)</Label>
              <Input id="invite-tailnet" value={tailnet} autoComplete="off" placeholder="noor@github" aria-describedby="invite-tailnet-hint" onChange={(e) => setTailnet(e.target.value)} />
              <p id="invite-tailnet-hint" className="text-xs text-muted-foreground">
                Links a Tailscale identity.
              </p>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={reset}>
                Cancel
              </Button>
              <Button type="submit">Review invite</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ChangeDialog
        open={phase === "plan"}
        onOpenChange={(next) => (next ? undefined : invitation !== null || missing ? setPhase("link") : reset())}
        title={`Invite ${input.username}`}
        target={{ name: "core.user.invite", version: 1 }}
        input={input}
        onApplied={(change) => {
          if (!applied(change)) return
          void client.invalidateQueries({ queryKey: USERS_QUERY_KEY })
          const inv = invitationOf(change)
          if (inv === null) setMissing(true)
          else setInvitation(inv)
          setPhase("link")
        }}
      />
      <LinkDialog username={input.username} invitation={phase === "link" ? invitation : null} onClose={done} />
      {phase === "link" && missing && (
        <Dialog open onOpenChange={(next) => (next ? undefined : done())}>
          <DialogContent>
            <DialogHeader>
              <DialogTitle>{`Enrollment link for ${input.username}`}</DialogTitle>
              <DialogDescription>The user was invited, but the answer held no link.</DialogDescription>
            </DialogHeader>
            <Alert variant="destructive" role="alert">
              <AlertDescription>Use New enrollment link in the user's menu to make one.</AlertDescription>
            </Alert>
            <DialogFooter>
              <Button onClick={done}>Done</Button>
            </DialogFooter>
          </DialogContent>
        </Dialog>
      )}
    </>
  )
}
