import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { DockerChange } from "./docker-change"
import { DOCKER_MODULE, dockerKey, type DockerImage } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

const UNITS = ["B", "KiB", "MiB", "GiB", "TiB"]

function bytes(n: number): string {
  let value = n
  let unit = 0
  while (Math.round(value * 10) / 10 >= 1024 && unit < UNITS.length - 1) {
    value /= 1024
    unit++
  }
  return `${Math.round(value * 10) / 10} ${UNITS[unit]}`
}

// imageName is what the daemon is asked about: the first tag, else the id.
function imageName(i: DockerImage): string {
  return i.tags[0] ?? i.id
}

function ImageTable({ images, onRemove }: { images: DockerImage[]; onRemove: (i: DockerImage) => void }) {
  return (
    <Table aria-label="Docker images">
      <TableHeader>
        <TableRow>
          <TableHead>Image</TableHead>
          <TableHead>ID</TableHead>
          <TableHead>Size</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>Used by</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {images.map((i) => (
          <TableRow key={i.id}>
            <TableCell className="font-mono">
              {i.tags.length === 0 ? "<none>" : i.tags.join(", ")}
              {i.dangling ? (
                <Badge variant="secondary" size="sm" className="ml-2">
                  dangling
                </Badge>
              ) : null}
            </TableCell>
            <TableCell className="font-mono">{i.id.replace(/^sha256:/, "").slice(0, 12)}</TableCell>
            <TableCell>{bytes(i.size)}</TableCell>
            <TableCell>{i.created.slice(0, 10)}</TableCell>
            <TableCell>{i.containers.length === 0 ? "—" : i.containers.join(", ")}</TableCell>
            <TableCell>
              <Button size="sm" variant="outline" aria-label={`Remove image ${imageName(i)}`} disabled={i.containers.length > 0} onClick={() => onRemove(i)}>
                Remove
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

// ImagesTab lists the host's Docker images and pulls, removes or prunes
// through the change dialog.
export function ImagesTab({ hostId }: { hostId: string }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const [pulling, setPulling] = useState(false)
  const [image, setImage] = useState("")
  const query = useQuery({
    queryKey: dockerKey(hostId, "images"),
    queryFn: () => readResource<{ images: DockerImage[] }>(hostId, DOCKER_MODULE, "images"),
  })

  const images = query.data?.images ?? []
  const prunable = images.some((i) => i.dangling && i.containers.length === 0)
  const reference = image.trim()

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (reference === "") return
    setPulling(false)
    setPending({ action: "docker.image.pull", title: `Pull ${reference}`, input: { image: reference } })
  }

  let content
  if (query.isPending) {
    content = <Spinner aria-label="Loading the images" />
  } else if (query.isError) {
    content = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(query.error)}</AlertDescription>
      </Alert>
    )
  } else if (images.length === 0) {
    content = <EmptyState title="No images" />
  } else {
    content = (
      <ImageTable
        images={images}
        onRemove={(i) => setPending({ action: "docker.image.remove", title: `Remove image ${imageName(i)}`, input: { image: imageName(i) } })}
      />
    )
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex justify-end gap-2">
        <Button onClick={() => setPulling(true)}>Pull image</Button>
        <Button variant="outline" disabled={!prunable} onClick={() => setPending({ action: "docker.image.prune", title: "Prune dangling images", input: {} })}>
          Prune images
        </Button>
      </div>
      {content}
      <Dialog open={pulling} onOpenChange={setPulling}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Pull an image</DialogTitle>
            <DialogDescription>The next step shows what the pull changes before anything is applied.</DialogDescription>
          </DialogHeader>
          <form aria-label="Pull an image" className="flex flex-col gap-4" onSubmit={submit}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="image-pull-reference">Image</Label>
              <Input id="image-pull-reference" value={image} placeholder="nginx:1.27" autoComplete="off" onChange={(e) => setImage(e.target.value)} />
            </div>
            <DialogFooter>
              <Button type="submit" disabled={reference === ""}>
                Plan pull
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      {pending ? (
        <DockerChange
          hostId={hostId}
          action={pending.action}
          title={pending.title}
          input={pending.input}
          onClose={() => {
            if (pending.action === "docker.image.pull") setImage("")
            setPending(null)
          }}
        />
      ) : null}
    </div>
  )
}
