import { useId, useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { readResource, request } from "@/api/client"
import { dockerQueryKey, FIREWALL_MODULE, statusQueryKey, type DockerStatus, type FirewallStatus } from "@/modules/firewall/types"
import { NpmChange, NpmControllerChange } from "./npm-change"
import { messageOf, readInstanceStatus } from "./status"
import { NPM_DEPLOY_PUBLIC_VERSION, npmKey, type AdminPassword, type InstanceStatus } from "./types"

type Pending = null | { scope: "host" | "controller"; action: string; version?: number; title: string; input: Record<string, unknown> }

type PlanChange = (pending: NonNullable<Pending>) => void

const ACCESS_LIST_ID = /^[1-9][0-9]*$/

function isAdminPassword(v: unknown): v is AdminPassword {
  return typeof v === "object" && v !== null && typeof (v as AdminPassword).email === "string" && typeof (v as AdminPassword).password === "string"
}

// DeployForm deploys NPM through npm.instance.deploy-public v2. The composite
// opens ports 80 and 443 for the NPM container when the host's Docker firewall
// integration is on, and otherwise as UFW rules when Darkpanel can manage the
// host's firewall, for IPv6 too when UFW's IPv6 setting is on. So the form
// reads firewall.docker and firewall.status first and sends what it read.
function DeployForm({ hostId, onPlan }: { hostId: string; onPlan: PlanChange }) {
  const base = useId()
  const [email, setEmail] = useState("")
  const docker = useQuery({
    queryKey: dockerQueryKey(hostId),
    queryFn: () => readResource<DockerStatus>(hostId, FIREWALL_MODULE, "docker"),
  })
  const status = useQuery({
    queryKey: statusQueryKey(hostId),
    queryFn: () => readResource<FirewallStatus>(hostId, FIREWALL_MODULE, "status"),
  })
  const integration = docker.isSuccess && docker.data.enabled && docker.data.supported
  const firewall = status.isSuccess && status.data.supported
  const ready = email.trim() !== "" && docker.isSuccess && status.isSuccess

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    onPlan({
      scope: "controller",
      action: "npm.instance.deploy-public",
      version: NPM_DEPLOY_PUBLIC_VERSION,
      title: "Deploy Nginx Proxy Manager",
      input: { hostId, acmeEmail: email.trim(), integration, firewall, ipv6: status.data.configured.ipv6 },
    })
  }

  return (
    <div className="flex flex-col gap-4">
      <p>Nginx Proxy Manager is not deployed on this host.</p>
      <form aria-label="Deploy" className="flex max-w-md flex-col gap-4" onSubmit={submit}>
        <div className="flex flex-col gap-2">
          <Label htmlFor={`${base}-email`}>Let&apos;s Encrypt email</Label>
          <Input id={`${base}-email`} type="email" autoComplete="off" value={email} onChange={(e) => setEmail(e.target.value)} />
        </div>
        <div className="flex flex-col gap-2">
          {(docker.isPending || status.isPending) && <Spinner aria-label="Loading the firewall state" />}
          {[docker, status].map(
            (read, i) =>
              read.isError && (
                <Alert key={i} variant="destructive" role="alert">
                  <AlertDescription>{messageOf(read.error)}</AlertDescription>
                </Alert>
              ),
          )}
          {docker.isSuccess && status.isSuccess && (
            <p className="text-sm text-muted-foreground">
              {integration
                ? "The Docker firewall integration is on: ports 80 and 443 are opened for the NPM container."
                : firewall
                  ? "The Docker firewall integration is off: the deploy allows ports 80 and 443 in the firewall as its last step. Then check them from outside and confirm that the instance is public."
                  : "The Docker firewall integration is off: open ports 80 and 443 to the internet yourself, then confirm that the instance is public."}
            </p>
          )}
        </div>
        <div>
          <Button type="submit" disabled={!ready}>
            Plan
          </Button>
        </div>
      </form>
    </div>
  )
}

function StatusRows({ status }: { status: InstanceStatus }) {
  const exposure = status.adminExposure
  const rows: { label: string; value: string }[] = [
    { label: "Status", value: status.running ? "running" : "stopped" },
    { label: "API", value: status.apiReady ? "ready" : "not ready" },
    { label: "Image", value: status.image },
    { label: "Pinned image", value: status.pinnedImage },
    { label: "Admin email", value: status.adminEmail },
    { label: "Public", value: status.public === "confirmed" ? "confirmed" : "not confirmed" },
    { label: "Admin interface", value: exposure !== null && exposure !== undefined ? `https://${exposure.hostname}` : "not exposed" },
  ]
  return (
    <dl aria-label="NPM instance" className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
      {rows.map((row) => (
        <div key={row.label} className="contents">
          <dt className="text-muted-foreground">{row.label}</dt>
          <dd className="break-all">{row.value}</dd>
        </div>
      ))}
    </dl>
  )
}

function ExposeDialog({ hostId, onClose, onPlan }: { hostId: string; onClose: () => void; onPlan: PlanChange }) {
  const base = useId()
  const [hostname, setHostname] = useState("")
  const [accessList, setAccessList] = useState("")
  const ready = hostname.trim() !== "" && (accessList === "" || ACCESS_LIST_ID.test(accessList))

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    const host = hostname.trim().toLowerCase()
    onPlan({
      scope: "controller",
      action: "npm.admin.expose",
      title: `Expose the admin interface at ${host}`,
      input: { hostId, hostname: host, ...(accessList !== "" && { accessListId: Number(accessList) }) },
    })
  }

  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onClose())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Expose the admin interface</DialogTitle>
          <DialogDescription>
            Publishes the NPM admin interface over HTTPS on a hostname you choose. A warning stays on every page while it is exposed.
          </DialogDescription>
        </DialogHeader>
        <form aria-label="Admin exposure" className="flex flex-col gap-4" onSubmit={submit}>
          <div className="flex flex-col gap-2">
            <Label htmlFor={`${base}-hostname`}>Hostname</Label>
            <Input id={`${base}-hostname`} autoComplete="off" value={hostname} onChange={(e) => setHostname(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor={`${base}-access-list`}>Access list ID (optional)</Label>
            <Input id={`${base}-access-list`} inputMode="numeric" autoComplete="off" value={accessList} onChange={(e) => setAccessList(e.target.value)} />
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={!ready}>
              Plan
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RevealDialog({ credentials, onHide }: { credentials: AdminPassword; onHide: () => void }) {
  return (
    <Dialog open onOpenChange={(open) => (open ? undefined : onHide())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>NPM admin credentials</DialogTitle>
          <DialogDescription>Shown once. Closing this dialog hides the password.</DialogDescription>
        </DialogHeader>
        <dl className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
          <div className="contents">
            <dt className="text-muted-foreground">Email</dt>
            <dd className="break-all">{credentials.email}</dd>
          </div>
          <div className="contents">
            <dt className="text-muted-foreground">Password</dt>
            <dd className="break-all font-mono">{credentials.password}</dd>
          </div>
        </dl>
        <DialogFooter>
          <Button onClick={onHide}>Hide password</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// InstancePanel is the Instance tab of the NPM page: the instance's state and
// the changes it offers, or the deploy form while none is deployed. The
// admin password is fetched on demand and held only in component state, so
// it never enters the query cache.
export function InstancePanel({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending>(null)
  const [dialog, setDialog] = useState<null | "expose" | "reveal">(null)
  const [revealed, setRevealed] = useState<AdminPassword | null>(null)
  const [revealError, setRevealError] = useState<string | null>(null)
  const [revealing, setRevealing] = useState(false)
  const query = useQuery({ queryKey: npmKey(hostId, "instance.status"), queryFn: () => readInstanceStatus(hostId) })

  function hide() {
    setRevealed(null)
    setDialog(null)
  }

  async function reveal() {
    setRevealing(true)
    setRevealError(null)
    try {
      const body = await request<{ value: unknown }>(
        "POST",
        "/api/v1/hosts/" + encodeURIComponent(hostId) + "/actions/npm.admin.reveal-password/v1/reveal",
        { input: {} },
      )
      if (!isAdminPassword(body.value)) throw new Error("The host answered with unexpected NPM admin credentials.")
      setRevealed({ email: body.value.email, password: body.value.password })
      setDialog("reveal")
    } catch (error) {
      setRevealError(messageOf(error))
    } finally {
      setRevealing(false)
    }
  }

  if (query.data === undefined) {
    if (query.isPending) return <Spinner aria-label="Loading the NPM instance" />
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{messageOf(query.error)}</AlertDescription>
      </Alert>
    )
  }

  const status = query.data
  const exposure = status.adminExposure
  const host = (action: string, title: string) => setPending({ scope: "host", action, title, input: {} })

  return (
    <div className="flex flex-col gap-4">
      {status.deployed ? (
        <>
          <StatusRows status={status} />
          {status.public !== "confirmed" && (
            <Alert variant="warning" role="status">
              <AlertDescription>Public access is not confirmed. Confirm only when ports 80 and 443 reach this host from the internet.</AlertDescription>
            </Alert>
          )}
          <div className="flex flex-wrap items-center gap-2">
            {status.public !== "confirmed" && (
              <Button variant="outline" onClick={() => host("npm.instance.confirm-public", "Confirm that the instance is public")}>
                Confirm public
              </Button>
            )}
            <Button variant="outline" onClick={() => host("npm.instance.upgrade", "Upgrade Nginx Proxy Manager")}>
              Upgrade
            </Button>
            <Button variant="outline" onClick={() => host("npm.instance.remove", "Remove Nginx Proxy Manager")}>
              Remove instance
            </Button>
            <Button variant="outline" disabled={revealing} onClick={() => void reveal()}>
              Reveal admin password
            </Button>
            {exposure !== null && exposure !== undefined ? (
              <Button
                variant="outline"
                onClick={() =>
                  setPending({
                    scope: "controller",
                    action: "npm.admin.unexpose",
                    title: `Remove the admin exposure at ${exposure.hostname}`,
                    input: { hostId, hostname: exposure.hostname },
                  })
                }
              >
                Remove admin exposure
              </Button>
            ) : (
              <Button variant="outline" onClick={() => setDialog("expose")}>
                Expose admin interface
              </Button>
            )}
            {status.upgradeAvailable && <Badge variant="secondary">Upgrade available</Badge>}
          </div>
          {revealError !== null && (
            <Alert variant="destructive" role="alert">
              <AlertDescription>{revealError}</AlertDescription>
            </Alert>
          )}
        </>
      ) : (
        <DeployForm hostId={hostId} onPlan={setPending} />
      )}
      {dialog === "expose" && (
        <ExposeDialog
          hostId={hostId}
          onClose={() => setDialog(null)}
          onPlan={(change) => {
            setDialog(null)
            setPending(change)
          }}
        />
      )}
      {dialog === "reveal" && revealed !== null && <RevealDialog credentials={revealed} onHide={hide} />}
      {/* Both change dialogs refetch ["resource", hostId] themselves on close; a second invalidation here would cancel that read and send another. */}
      {pending?.scope === "host" && (
        <NpmChange hostId={hostId} action={pending.action} title={pending.title} input={pending.input} onClose={() => setPending(null)} />
      )}
      {pending?.scope === "controller" && (
        <NpmControllerChange action={pending.action} version={pending.version} title={pending.title} input={pending.input} onClose={() => setPending(null)} />
      )}
    </div>
  )
}
