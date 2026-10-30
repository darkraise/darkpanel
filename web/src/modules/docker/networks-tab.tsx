import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { DockerChange } from "./docker-change"
import { DOCKER_MODULE, dockerKey, type DockerNetwork } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

interface Draft {
  name: string
  driver: string
  subnet: string
  gateway: string
  internal: boolean
  attachable: boolean
  ipv6: boolean
}

const EMPTY: Draft = { name: "", driver: "", subnet: "", gateway: "", internal: false, attachable: false, ipv6: false }

// createInput is the docker.network.create input a draft describes; an empty
// optional field and an unticked box are left out.
function createInput(d: Draft): Record<string, unknown> {
  const input: Record<string, unknown> = { name: d.name.trim() }
  if (d.driver.trim() !== "") input.driver = d.driver.trim()
  if (d.subnet.trim() !== "") input.subnet = d.subnet.trim()
  if (d.gateway.trim() !== "") input.gateway = d.gateway.trim()
  if (d.internal) input.internal = true
  if (d.attachable) input.attachable = true
  if (d.ipv6) input.ipv6 = true
  return input
}

function flags(n: DockerNetwork): string {
  const set: string[] = []
  if (n.internal) set.push("internal")
  if (n.ipv6) set.push("IPv6")
  return set.join(", ")
}

function Text({ id, label, value, placeholder, onChange }: {
  id: string
  label: string
  value: string
  placeholder?: string
  onChange: (value: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} placeholder={placeholder} autoComplete="off" onChange={(e) => onChange(e.target.value)} />
    </div>
  )
}

function Flag({ id, label, checked, onChange }: { id: string; label: string; checked: boolean; onChange: (checked: boolean) => void }) {
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} checked={checked} onCheckedChange={(c) => onChange(c === true)} />
      <Label htmlFor={id}>{label}</Label>
    </div>
  )
}

// NetworkForm holds one opening's draft; it mounts when the dialog opens, so
// the next opening starts empty.
function NetworkForm({ onPlan }: { onPlan: (input: Record<string, unknown>) => void }) {
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const set = <K extends keyof Draft>(key: K, value: Draft[K]) => setDraft((d) => ({ ...d, [key]: value }))

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    onPlan(createInput(draft))
  }

  return (
    <form aria-label="Network" className="flex flex-col gap-4" onSubmit={submit}>
      <Text id="network-name" label="Name" value={draft.name} onChange={(v) => set("name", v)} />
      <Text id="network-driver" label="Driver" value={draft.driver} placeholder="bridge" onChange={(v) => set("driver", v)} />
      <Text id="network-subnet" label="Subnet" value={draft.subnet} placeholder="10.20.0.0/24" onChange={(v) => set("subnet", v)} />
      <Text id="network-gateway" label="Gateway" value={draft.gateway} placeholder="10.20.0.1" onChange={(v) => set("gateway", v)} />
      <Flag id="network-internal" label="Internal" checked={draft.internal} onChange={(v) => set("internal", v)} />
      <Flag id="network-attachable" label="Attachable" checked={draft.attachable} onChange={(v) => set("attachable", v)} />
      <Flag id="network-ipv6" label="IPv6" checked={draft.ipv6} onChange={(v) => set("ipv6", v)} />
      <DialogFooter>
        <Button type="submit" disabled={draft.name.trim() === ""}>Plan</Button>
      </DialogFooter>
    </form>
  )
}

function CreateNetwork({ onPlan }: { onPlan: (input: Record<string, unknown>) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>Create network</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create a network</DialogTitle>
          <DialogDescription>The next step shows what the network changes before anything is applied.</DialogDescription>
        </DialogHeader>
        <NetworkForm
          onPlan={(input) => {
            onPlan(input)
            setOpen(false)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}

function NetworkTable({ networks, onRemove }: { networks: DockerNetwork[]; onRemove: (n: DockerNetwork) => void }) {
  if (networks.length === 0) return <EmptyState title="No networks" />
  return (
    <Table aria-label="Docker networks">
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Driver</TableHead>
          <TableHead>Scope</TableHead>
          <TableHead>Flags</TableHead>
          <TableHead>Subnets</TableHead>
          <TableHead>Containers</TableHead>
          <TableHead>Owner</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {networks.map((n) => (
          <TableRow key={n.id}>
            <TableCell>{n.name}</TableCell>
            <TableCell>{n.driver}</TableCell>
            <TableCell>{n.scope}</TableCell>
            <TableCell>{flags(n)}</TableCell>
            <TableCell className="font-mono">{n.subnets.join(", ")}</TableCell>
            <TableCell>{n.containers.join(", ")}</TableCell>
            <TableCell>{n.owner ?? ""}</TableCell>
            <TableCell>
              <Button
                size="sm"
                variant="outline"
                aria-label={`Remove ${n.name}`}
                disabled={n.owner !== undefined && n.owner !== ""}
                onClick={() => onRemove(n)}
              >
                Remove
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// NetworksTab lists the host's Docker networks and creates or removes one
// through the change dialog.
export function NetworksTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const query = useQuery({
    queryKey: dockerKey(hostId, "networks"),
    queryFn: () => readResource<{ networks: DockerNetwork[] }>(hostId, DOCKER_MODULE, "networks"),
  })

  let content
  if (query.isPending) {
    content = <Spinner aria-label="Loading networks" />
  } else if (query.isError) {
    content = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  } else {
    content = (
      <NetworkTable
        networks={query.data.networks}
        onRemove={(n) => setPending({ action: "docker.network.remove", title: `Remove network ${n.name}`, input: { network: n.name } })}
      />
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex justify-end">
        <CreateNetwork
          onPlan={(input) => setPending({ action: "docker.network.create", title: `Create network ${String(input.name)}`, input })}
        />
      </div>
      {content}
      <DockerChange
        hostId={hostId}
        action={pending?.action ?? "docker.network.create"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </div>
  )
}
