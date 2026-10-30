import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { formatTime } from "@/lib/format"
import { DockerChange } from "./docker-change"
import { DOCKER_MODULE, dockerKey, type DockerVolume } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
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

// VolumeForm holds one opening's draft; it mounts when the dialog opens, so
// the next opening starts empty.
function VolumeForm({ onPlan }: { onPlan: (input: Record<string, unknown>) => void }) {
  const [name, setName] = useState("")
  const [driver, setDriver] = useState("")

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const input: Record<string, unknown> = { name: name.trim() }
    if (driver.trim() !== "") input.driver = driver.trim()
    onPlan(input)
  }

  return (
    <form aria-label="Volume" className="flex flex-col gap-4" onSubmit={submit}>
      <Text id="volume-name" label="Name" value={name} onChange={setName} />
      <Text id="volume-driver" label="Driver" value={driver} placeholder="local" onChange={setDriver} />
      <DialogFooter>
        <Button type="submit" disabled={name.trim() === ""}>Plan</Button>
      </DialogFooter>
    </form>
  )
}

function CreateVolume({ onPlan }: { onPlan: (input: Record<string, unknown>) => void }) {
  const [open, setOpen] = useState(false)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>Create volume</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create a volume</DialogTitle>
          <DialogDescription>The next step shows what the volume changes before anything is applied.</DialogDescription>
        </DialogHeader>
        <VolumeForm
          onPlan={(input) => {
            onPlan(input)
            setOpen(false)
          }}
        />
      </DialogContent>
    </Dialog>
  )
}

function VolumeTable({ volumes, onRemove }: { volumes: DockerVolume[]; onRemove: (v: DockerVolume) => void }) {
  if (volumes.length === 0) return <EmptyState title="No volumes" />
  return (
    <Table aria-label="Docker volumes">
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Driver</TableHead>
          <TableHead>Mountpoint</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>Containers</TableHead>
          <TableHead>Owner</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {volumes.map((v) => (
          <TableRow key={v.name}>
            <TableCell>{v.name}</TableCell>
            <TableCell>{v.driver}</TableCell>
            <TableCell className="font-mono">{v.mountpoint}</TableCell>
            <TableCell>{formatTime(v.created)}</TableCell>
            <TableCell>{v.containers.join(", ")}</TableCell>
            <TableCell>{v.owner ?? ""}</TableCell>
            <TableCell>
              <Button
                size="sm"
                variant="outline"
                aria-label={`Remove ${v.name}`}
                disabled={v.owner !== undefined && v.owner !== ""}
                onClick={() => onRemove(v)}
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

// VolumesTab lists the host's Docker volumes and creates or removes one
// through the change dialog.
export function VolumesTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const query = useQuery({
    queryKey: dockerKey(hostId, "volumes"),
    queryFn: () => readResource<{ volumes: DockerVolume[] }>(hostId, DOCKER_MODULE, "volumes"),
  })

  let content
  if (query.isPending) {
    content = <Spinner aria-label="Loading volumes" />
  } else if (query.isError) {
    content = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  } else {
    content = (
      <VolumeTable
        volumes={query.data.volumes}
        onRemove={(v) => setPending({ action: "docker.volume.remove", title: `Remove volume ${v.name}`, input: { volume: v.name } })}
      />
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex justify-end">
        <CreateVolume
          onPlan={(input) => setPending({ action: "docker.volume.create", title: `Create volume ${String(input.name)}`, input })}
        />
      </div>
      {content}
      <DockerChange
        hostId={hostId}
        action={pending?.action ?? "docker.volume.create"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </div>
  )
}
