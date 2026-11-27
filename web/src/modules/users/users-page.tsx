import { useRef, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { MoreHorizontal, UserPlus } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Textarea } from "darkraise-ui/components/textarea"
import { HOSTS_QUERY_KEY, listHosts, readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { FALLBACK_FOCUS, useFocusFallback, useMenuDialogFocus, useReopenFocus } from "@/lib/return-focus"
import { applied } from "@/lib/users"
import { useCanOnHost } from "@/session/can"
import { keyError, nameError, passwordError, type Account } from "./keys"
import { SshdCard } from "./sshd-card"
import { UserSheet } from "./user-sheet"

// Pending is one change a menu or form asks for, planned in ChangeDialog.
interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
  done: string
}

function FieldError({ id, children }: { id: string; children: string }) {
  return (
    <p id={id} className="text-sm text-destructive-text">
      {children}
    </p>
  )
}

// PasswordFields shows each field's error once it holds text or the form
// was submitted.
function PasswordFields({ password, confirm, onPassword, onConfirm, submitted }: {
  password: string
  confirm: string
  onPassword: (v: string) => void
  onConfirm: (v: string) => void
  submitted: boolean
}) {
  const all = passwordError(password, confirm)
  const errors = { password: submitted || password !== "" ? all.password : null, confirm: submitted || confirm !== "" ? all.confirm : null }
  return (
    <>
      <div className="flex flex-col gap-2">
        <Label htmlFor="user-password">Password</Label>
        <Input
          id="user-password"
          type="password"
          autoComplete="new-password"
          value={password}
          aria-invalid={errors.password !== null}
          aria-describedby={errors.password !== null ? "user-password-error" : undefined}
          onChange={(e) => onPassword(e.target.value)}
        />
        {errors.password !== null && <FieldError id="user-password-error">{errors.password}</FieldError>}
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="user-password-confirm">Confirm the password</Label>
        <Input
          id="user-password-confirm"
          type="password"
          autoComplete="new-password"
          value={confirm}
          aria-invalid={errors.confirm !== null}
          aria-describedby={errors.confirm !== null ? "user-password-confirm-error" : undefined}
          onChange={(e) => onConfirm(e.target.value)}
        />
        {errors.confirm !== null && <FieldError id="user-password-confirm-error">{errors.confirm}</FieldError>}
      </div>
    </>
  )
}

// AddUserDialog collects users.user.create's input; it unmounts when it
// closes, so the password it held is gone.
// A form stays mounted, hidden, while its change is reviewed, so a refused
// plan returns to it with every value as typed.
interface FormProps {
  hostName: string
  hidden: boolean
  onCancel: () => void
  onReview: (p: Pending) => void
}

function AddUserDialog({ taken, hostName, hidden, onCancel, onReview }: FormProps & { taken: string[] }) {
  const reviewButton = useReopenFocus(hidden)
  const [name, setName] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [sudo, setSudo] = useState(false)
  const [key, setKey] = useState("")
  const [submitted, setSubmitted] = useState(false)
  const nameProblem = name === "" ? (submitted ? "Enter a name" : null) : taken.includes(name) ? `An account named ${name} exists` : nameError(name)
  const pw = passwordError(password, confirm)
  const keyProblem = key.trim() === "" ? null : keyError(key)

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setSubmitted(true)
    if (name === "" || nameProblem !== null || pw.password !== null || pw.confirm !== null || keyProblem !== null) return
    const input: Record<string, unknown> = { name }
    if (sudo) input.sudo = true
    if (password !== "") input.password = password
    if (key.trim() !== "") input.keys = [key.trim()]
    onReview({ action: "users.user.create", title: `Add user ${name}`, input, done: `${name} added` })
  }

  return (
    <Dialog open={!hidden} onOpenChange={(open) => (open ? undefined : onCancel())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Add a user on ${hostName}`}</DialogTitle>
          <DialogDescription>A login account with its home and group.</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          <div className="flex flex-col gap-2">
            <Label htmlFor="user-name">Name</Label>
            <Input
              id="user-name"
              autoComplete="off"
              spellCheck={false}
              value={name}
              aria-invalid={nameProblem !== null}
              aria-describedby={nameProblem !== null ? "user-name-error" : undefined}
              onChange={(e) => setName(e.target.value)}
            />
            {nameProblem !== null && <FieldError id="user-name-error">{nameProblem}</FieldError>}
          </div>
          <PasswordFields password={password} confirm={confirm} onPassword={setPassword} onConfirm={setConfirm} submitted={submitted} />
          <div className="flex items-center gap-2">
            <Checkbox id="user-sudo" checked={sudo} onCheckedChange={(v) => setSudo(v === true)} />
            <Label htmlFor="user-sudo">Can use sudo</Label>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="user-new-key">Public key (optional)</Label>
            <Textarea
              id="user-new-key"
              rows={3}
              spellCheck={false}
              value={key}
              placeholder="ssh-ed25519 AAAA… comment"
              aria-invalid={keyProblem !== null}
              aria-describedby={keyProblem !== null ? "user-new-key-error" : undefined}
              onChange={(e) => setKey(e.target.value)}
              className="font-mono text-xs"
            />
            {keyProblem !== null && <FieldError id="user-new-key-error">{keyProblem}</FieldError>}
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button ref={reviewButton} type="submit">Review change</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function SetPasswordDialog({ name, hidden, onCancel, onReview }: Omit<FormProps, "hostName"> & { name: string }) {
  const reviewButton = useReopenFocus(hidden)
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")
  const [submitted, setSubmitted] = useState(false)
  const errors = passwordError(password, confirm)

  function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setSubmitted(true)
    if (password === "" || errors.password !== null || errors.confirm !== null) return
    onReview({ action: "users.password.set", title: `Set the password of ${name}`, input: { name, password }, done: `Password set for ${name}` })
  }

  return (
    <Dialog open={!hidden} onOpenChange={(open) => (open ? undefined : onCancel())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{`Set the password of ${name}`}</DialogTitle>
          <DialogDescription>It is never logged or shown again.</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={submit} noValidate>
          <PasswordFields password={password} confirm={confirm} onPassword={setPassword} onConfirm={setConfirm} submitted={submitted} />
          {submitted && password === "" && <FieldError id="user-password-empty">Enter a password</FieldError>}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button ref={reviewButton} type="submit">Review change</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

// menuOf is an account's menu for an admin; root keeps its keys and
// password but has no Lock, Delete or sudo item.
function menuOf(a: Account): [string, Pick][] {
  const n = a.name
  const items: [string, Pick][] = [["Keys", "keys"], ["Set password", "password"]]
  if (a.passwordSet) items.push(["Remove password", { action: "users.password.remove", title: `Remove the password of ${n}`, input: { name: n }, done: `Password removed for ${n}` }])
  if (a.uid === 0) return items
  items.push(
    a.sudo
      ? ["Remove from sudo", { action: "users.sudo.remove", title: `Remove ${n} from sudo`, input: { name: n }, done: `${n} removed from sudo` }]
      : ["Add to sudo", { action: "users.sudo.add", title: `Add ${n} to sudo`, input: { name: n }, done: `${n} added to sudo` }],
    a.locked
      ? ["Unlock", { action: "users.user.unlock", title: `Unlock ${n}`, input: { name: n }, done: `${n} unlocked` }]
      : ["Lock", { action: "users.user.lock", title: `Lock ${n}`, input: { name: n }, done: `${n} locked` }],
    ["Delete", { action: "users.user.delete", title: `Delete ${n}`, input: { name: n }, done: `${n} deleted` }],
  )
  return items
}

type Pick = Pending | "keys" | "password"

function AccountMenu({ account, onPick }: { account: Account; onPick: (a: Account, p: Pick) => void }) {
  const { trigger, pick, onCloseAutoFocus } = useMenuDialogFocus((p: Pick) => onPick(account, p))
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button ref={trigger} variant="ghost" size="icon" aria-label={`Actions for ${account.name}`}>
          <MoreHorizontal aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onCloseAutoFocus={onCloseAutoFocus}>
        {menuOf(account).map(([label, item]) => (
          <DropdownMenuItem key={label} onSelect={() => pick(item)}>
            {label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Loading() {
  return (
    <div className="flex flex-col gap-2" role="status" aria-label="Loading the users">
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-2/3" />
    </div>
  )
}

// UsersPage lists the host's login accounts and drives them, with the SSH
// daemon's card below (UI-3, UX-4, UX-5).
export function UsersPage({ hostId }: { hostId: string }) {
  const admin = useCanOnHost("admin")
  const client = useQueryClient()
  const key = ["resource", hostId, "users.user.list"]
  const accounts = useQuery({ queryKey: key, queryFn: () => readAction<{ accounts: Account[] }>("users.user.list", 1, {}, hostId) })
  const [adding, setAdding] = useState(false)
  const [passwordFor, setPasswordFor] = useState<string | null>(null)
  const [sheet, setSheet] = useState<string | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const sheetAccount = sheet === null ? undefined : accounts.data?.accounts.find((a) => a.name === sheet)
  useFocusFallback(adding || passwordFor !== null || sheet !== null || pending !== null)

  function pick(a: Account, p: Pick) {
    if (p === "keys") setSheet(a.name)
    else if (p === "password") setPasswordFor(a.name)
    else setPending(p)
  }

  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const hostName = hosts.data?.hosts.find((h) => h.id === hostId)?.name ?? hostId
  // Whether the reviewed change applied: only then does its form close.
  const done = useRef(false)

  function review(p: Pending) {
    done.current = false
    setPending(p)
  }

  function endReview() {
    setPending(null)
    if (done.current) {
      setAdding(false)
      setPasswordFor(null)
    }
  }

  let body
  if (accounts.isPending) body = <Loading />
  else if (accounts.isError)
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>Couldn't load users</span>
          <Button size="sm" variant="outline" onClick={() => void accounts.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  else
    body = (
      <Table aria-label="Login accounts">
        <TableHeader>
          <TableRow>
            <TableHead>Name</TableHead>
            <TableHead>Access</TableHead>
            <TableHead>Password</TableHead>
            <TableHead>Keys</TableHead>
            {admin && (
              <TableHead>
                <span className="sr-only">Actions</span>
              </TableHead>
            )}
          </TableRow>
        </TableHeader>
        <TableBody>
          {accounts.data.accounts.map((a) => (
            <TableRow key={a.name}>
              <TableCell className="max-w-[14rem] sm:max-w-[20rem]">
                <Button variant="link" className="h-auto max-w-full justify-start p-0 font-mono text-xs" title={a.name} onClick={() => setSheet(a.name)}>
                  <span className="truncate">{a.name}</span>
                </Button>
              </TableCell>
              <TableCell>
                <div className="flex flex-wrap gap-1">
                  {a.sudo && <Badge variant="outline">Sudo</Badge>}
                  {a.locked && <Badge variant="outline">Locked</Badge>}
                </div>
              </TableCell>
              <TableCell>{a.passwordSet ? "Set" : "None"}</TableCell>
              <TableCell>{a.keys}</TableCell>
              {admin && (
                <TableCell>
                  <AccountMenu account={a} onPick={pick} />
                </TableCell>
              )}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    )

  return (
    <div className="flex flex-col gap-6">
      <h2 className="sr-only">Users and SSH</h2>
      {admin && (
        <div className="flex justify-end">
          <Button {...{ [FALLBACK_FOCUS]: "" }} onClick={() => setAdding(true)}>
            <UserPlus aria-hidden="true" />
            Add user
          </Button>
        </div>
      )}
      <div className="overflow-x-auto">{body}</div>
      <SshdCard hostId={hostId} hostName={hostName} admin={admin} />
      {adding && (
        <AddUserDialog
          taken={accounts.data?.accounts.map((a) => a.name) ?? []}
          hostName={hostName}
          hidden={pending !== null}
          onCancel={() => setAdding(false)}
          onReview={review}
        />
      )}
      {passwordFor !== null && <SetPasswordDialog name={passwordFor} hidden={pending !== null} onCancel={() => setPasswordFor(null)} onReview={review} />}
      {sheetAccount !== undefined && <UserSheet hostId={hostId} account={sheetAccount} admin={admin} onClose={() => setSheet(null)} />}
      {pending !== null && (
        <ChangeDialog
          open
          onOpenChange={(open) => (open ? undefined : endReview())}
          title={pending.title}
          target={{ name: pending.action, version: 1, hostId, module: "users" }}
          input={pending.input}
          onApplied={(c: Change) => {
            void client.invalidateQueries({ queryKey: key })
            if (applied(c)) {
              done.current = true
              toast(pending.done)
            }
          }}
        />
      )}
    </div>
  )
}
