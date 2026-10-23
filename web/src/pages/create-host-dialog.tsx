import { Suspense, useState, type FormEvent } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Clipboard, ClipboardControl, ClipboardInput, ClipboardLabel, ClipboardTrigger } from "darkraise-ui/components/clipboard"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { HOSTS_QUERY_KEY, changeError, changeInProgress, listHosts } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { formatTime } from "@/lib/format"
import { hostCreateExtensions, moduleRegistry } from "@/modules/registry"
import type { ModuleEntry } from "@/modules/types"

// The enrollment grant core.host.create answers; its token is in the apply
// response only, so it is shown here once and then forgotten.
interface Grant {
  host: { id: string; name: string }
  token: string
  expiresAt: string
}

type Phase = "closed" | "form" | "plan" | "grant"

function isGrant(output: unknown): output is Grant {
  if (typeof output !== "object" || output === null) return false
  const o = output as Record<string, unknown>
  return typeof o.token === "string" && typeof o.expiresAt === "string" && typeof o.host === "object" && o.host !== null
}

// CreateHostDialog adds a host: it asks for the name, renders the host
// creation inputs of every module enabled on the controller's own host,
// plans and applies core.host.create through the change dialog, and shows
// the enrollment token once.
export function CreateHostDialog({ entries = moduleRegistry }: { entries?: ModuleEntry[] }) {
  const client = useQueryClient()
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const [phase, setPhase] = useState<Phase>("closed")
  const [values, setValues] = useState<Record<string, Record<string, unknown>>>({})
  const [input, setInput] = useState<Record<string, unknown>>({})
  const [grant, setGrant] = useState<Grant | null>(null)
  const [grantMissing, setGrantMissing] = useState(false)

  const localHost = hosts.data?.hosts.find((h) => h.isLocal)
  const extensions = localHost === undefined ? [] : hostCreateExtensions(entries, localHost)

  function reset() {
    setPhase("closed")
    setValues({})
    setInput({})
    setGrant(null)
    setGrantMissing(false)
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const name = String(form.get("name") ?? "").trim()
    const controllerAddress = String(form.get("controllerAddress") ?? "").trim()
    const merged = extensions.reduce<Record<string, unknown>>((acc, e) => ({ ...acc, ...(values[e.id] ?? {}) }), {})
    merged.name = name
    if (controllerAddress !== "") merged.controllerAddress = controllerAddress
    setInput(merged)
    setPhase("plan")
  }

  function applied(change: Change) {
    if (changeError(change) !== null || changeInProgress(change)) return
    // The token exists only in this answer, so a host created without one
    // must say so rather than read as a plain success.
    if (isGrant(change.output)) setGrant(change.output)
    else setGrantMissing(true)
    setPhase("grant")
    void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
  }

  return (
    <>
      {/* The module inputs depend on the controller's own host, so the form waits for the host list. */}
      <Button disabled={!hosts.isSuccess} onClick={() => setPhase("form")}>
        Add host
      </Button>
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? setPhase("form") : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add a host</DialogTitle>
            <DialogDescription>The host gets a one-hour enrollment token for its agent.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" onSubmit={submit}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="host-name">Name</Label>
              <Input id="host-name" name="name" required maxLength={63} pattern="[a-z0-9][a-z0-9.\-]{0,62}" autoComplete="off" />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="host-controller-address">Controller address (optional)</Label>
              <Input id="host-controller-address" name="controllerAddress" placeholder="controller.example:7443" autoComplete="off" />
            </div>
            {extensions.map((ext) => {
              const Extension = ext.render
              return (
                <Suspense key={ext.id} fallback={<Spinner aria-label="Loading the module's inputs" />}>
                  <Extension value={values[ext.id] ?? {}} onChange={(v) => setValues((cur) => ({ ...cur, [ext.id]: v }))} />
                </Suspense>
              )
            })}
            <DialogFooter>
              <Button type="submit">Continue</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ChangeDialog
        open={phase === "plan"}
        onOpenChange={(next) => {
          if (!next) reset()
        }}
        title="Add a host"
        target={{ name: "core.host.create", version: 1 }}
        input={input}
        onApplied={applied}
      />
      <Dialog open={phase === "grant"} onOpenChange={(next) => (next ? undefined : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Host added</DialogTitle>
            <DialogDescription>
              {grantMissing ? "The host exists, but it cannot enroll with this answer." : "Copy the enrollment token now. It will not be shown again."}
            </DialogDescription>
          </DialogHeader>
          {grantMissing && (
            <div className="flex flex-col gap-4">
              <Alert variant="destructive" role="alert">
                <AlertDescription>The host was created, but the controller did not return its enrollment token.</AlertDescription>
              </Alert>
              <DialogFooter>
                <Button onClick={reset}>Done</Button>
              </DialogFooter>
            </div>
          )}
          {grant !== null && (
            <div className="flex flex-col gap-4">
              <Clipboard value={grant.token}>
                <ClipboardLabel>{`Enrollment token for ${grant.host.name}`}</ClipboardLabel>
                <ClipboardControl>
                  <ClipboardInput className="font-mono" />
                  <ClipboardTrigger>Copy</ClipboardTrigger>
                </ClipboardControl>
              </Clipboard>
              <p className="text-sm text-muted-foreground">
                {`On the new host, run sudo darkpanel agent enroll and paste the token before ${formatTime(grant.expiresAt)}.`}
              </p>
              <DialogFooter>
                <Button onClick={reset}>Done</Button>
              </DialogFooter>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  )
}
