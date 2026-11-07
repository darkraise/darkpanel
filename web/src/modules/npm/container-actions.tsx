import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { readAction, readResource } from "@/api/client"
import { DOCKER_ACTION_VERSION, DOCKER_MODULE, dockerKey, type ContainerDetail, type DockerNetwork } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { Field, ReadFailure } from "./list-parts"
import { NpmControllerChange } from "./npm-change"
import { parseDomains, parseOptionalId, parsePort } from "./parse"
import { readInstanceStatus } from "./status"
import { npmKey } from "./types"

const UNSUPPORTED =
  "This container is not on a user-defined bridge network, so Nginx Proxy Manager cannot reach it by name: connect the container to a user-defined network."

interface Pending {
  title: string
  input: Record<string, unknown>
}

// targetNetworks lists, in name order, the container's networks NPM can
// reach it on by name: user-defined bridge networks. The default bridge
// network has no name resolution, so it is left out.
function targetNetworks(endpoints: string[], networks: DockerNetwork[]): string[] {
  const bridges = new Set(networks.filter((n) => n.driver === "bridge" && n.name !== "bridge").map((n) => n.name))
  return endpoints.filter((name) => bridges.has(name)).sort()
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v)
}

// The two guards below check only the fields ExposeForm reads; the replies
// stay whole because the Docker pages share these query keys.
function hasExposeDetail(v: unknown): v is ContainerDetail {
  return (
    isRecord(v) &&
    Array.isArray(v.networks) &&
    v.networks.every((e) => isRecord(e) && typeof e.network === "string") &&
    Array.isArray(v.ports) &&
    v.ports.every((p) => isRecord(p) && typeof p.privatePort === "number")
  )
}

function hasExposeNetworks(v: unknown): v is { networks: DockerNetwork[] } {
  return isRecord(v) && Array.isArray(v.networks) && v.networks.every((n) => isRecord(n) && typeof n.name === "string" && typeof n.driver === "string")
}

function readExposeDetail(hostId: string, containerId: string): Promise<ContainerDetail> {
  return readAction<unknown>("docker.container.inspect", DOCKER_ACTION_VERSION, { container: containerId }, hostId).then((body) => {
    if (!hasExposeDetail(body)) throw new Error("The host answered with an unexpected container.")
    return body
  })
}

function readExposeNetworks(hostId: string): Promise<{ networks: DockerNetwork[] }> {
  return readResource<unknown>(hostId, DOCKER_MODULE, "networks").then((body) => {
    if (!hasExposeNetworks(body)) throw new Error("The host answered with an unexpected list of Docker networks.")
    return body
  })
}

function lowestPort(ports: number[]): string {
  return ports.length === 0 ? "" : String(Math.min(...ports))
}

interface ExposeFormProps {
  container: ContainerRef
  endpoints: string[]
  networks: DockerNetwork[]
  ports: number[]
  onDone: () => void
}

function ExposeForm({ container, endpoints, networks, ports, onDone }: ExposeFormProps) {
  const name = container.name.replace(/^\//, "")
  const supported = targetNetworks(endpoints, networks)
  const [picked, setPicked] = useState("")
  const [portText, setPortText] = useState(() => lowestPort(ports))
  const [domainsText, setDomainsText] = useState("")
  const [websockets, setWebsockets] = useState(false)
  const [accessListText, setAccessListText] = useState("")
  const [pending, setPending] = useState<Pending | null>(null)
  // A pick that leaves the supported set is forgotten, so a later read that
  // brings the network back does not select it again by itself.
  if (picked !== "" && !supported.includes(picked)) setPicked("")

  // Without a supported network the plan still goes to the controller, so
  // its own refusal shows in the preview.
  const network = supported.includes(picked) ? picked : (supported[0] ?? [...endpoints].sort()[0] ?? "")
  const port = parsePort(portText)
  const domains = parseDomains(domainsText)
  const accessListId = parseOptionalId(accessListText)
  const ready = port !== null && domains.length > 0 && accessListId !== null

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    const input: Record<string, unknown> = { hostId: container.hostId, container: name, port, network, domains }
    if (websockets) input.websockets = true
    if (accessListId !== 0) input.accessListId = accessListId
    setPending({ title: `Expose ${name} at ${domains.join(", ")}`, input })
  }

  return (
    <>
      <form aria-label="Expose container" className="flex flex-col gap-4" onSubmit={submit}>
        {supported.length === 0 ? (
          <Alert variant="warning" role="status">
            <AlertDescription>{UNSUPPORTED}</AlertDescription>
          </Alert>
        ) : (
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">Network</legend>
            <RadioGroup value={network} onValueChange={setPicked} aria-label="Network">
              {supported.map((n) => (
                <div key={n} className="flex items-center gap-2">
                  <RadioGroupItem id={`expose-network-${n}`} value={n} />
                  <Label htmlFor={`expose-network-${n}`}>{n}</Label>
                </div>
              ))}
            </RadioGroup>
          </fieldset>
        )}
        <Field id="expose-port" label="Port" value={portText} invalid={portText !== "" && port === null} onChange={setPortText} />
        <Field id="expose-domains" label="Domains" value={domainsText} onChange={setDomainsText} help="Separate several names with commas or spaces." />
        <div className="flex items-center gap-2">
          <Checkbox id="expose-websockets" checked={websockets} onCheckedChange={(c) => setWebsockets(c === true)} />
          <Label htmlFor="expose-websockets">Websockets</Label>
        </div>
        <Field id="expose-acl" label="Access list ID (optional)" value={accessListText} invalid={accessListId === null} onChange={setAccessListText} />
        <div>
          <Button type="submit" disabled={!ready}>
            Plan
          </Button>
        </div>
      </form>
      <NpmControllerChange
        action="npm.expose.container"
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => {
          setPending(null)
          onDone()
        }}
      />
    </>
  )
}

function ExposeLoader({ container, onDone }: { container: ContainerRef; onDone: () => void }) {
  const { hostId, containerId } = container
  const detail = useQuery({
    queryKey: ["resource", hostId, "docker.container", containerId],
    queryFn: () => readExposeDetail(hostId, containerId),
  })
  const networks = useQuery({
    queryKey: dockerKey(hostId, "networks"),
    queryFn: () => readExposeNetworks(hostId),
  })
  if (detail.data === undefined || networks.data === undefined) {
    if (detail.isPending || networks.isPending) return <Spinner aria-label="Loading the container's networks" />
    return <ReadFailure error={detail.error ?? networks.error} />
  }
  return (
    <>
      {(detail.isError || networks.isError) && <ReadFailure error={detail.error ?? networks.error} />}
      <ExposeForm
        container={container}
        endpoints={detail.data.networks.map((e) => e.network)}
        networks={networks.data.networks}
        ports={detail.data.ports.map((p) => p.privatePort)}
        onDone={onDone}
      />
    </>
  )
}

// ExposeAction publishes a container through the host's Nginx Proxy Manager
// with npm.expose.container, once the host shows a deployed instance.
export function ExposeAction({ container, onDone }: { container: ContainerRef; onDone: () => void }) {
  const status = useQuery({ queryKey: npmKey(container.hostId, "instance.status"), queryFn: () => readInstanceStatus(container.hostId) })
  if (status.data === undefined) {
    if (status.isPending) return <Spinner aria-label="Loading Nginx Proxy Manager" />
    return <ReadFailure error={status.error} />
  }
  if (!status.data.deployed) return <p>Nginx Proxy Manager is not deployed on this host. Deploy it first.</p>
  return (
    <>
      {status.isError && <ReadFailure error={status.error} />}
      <ExposeLoader container={container} onDone={onDone} />
    </>
  )
}
