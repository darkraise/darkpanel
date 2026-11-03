import { useId, useState, type FormEvent } from "react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import type { DockerNetwork } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { CloudflareChange } from "./cloudflare-change"
import { tunnelLabel } from "./connector-types"

const UNSUPPORTED =
  "This container is not on a user-defined bridge network, so the Cloudflare connector cannot reach it by name: connect the container to a user-defined network."

interface Pending {
  title: string
  input: Record<string, unknown>
}

// targetNetworks lists, in name order, the container's networks the connector
// can reach it on by name: user-defined bridge networks. The default bridge
// network has no name resolution, so it is left out.
function targetNetworks(endpoints: string[], networks: DockerNetwork[]): string[] {
  const bridges = new Set(networks.filter((n) => n.driver === "bridge" && n.name !== "bridge").map((n) => n.name))
  return endpoints.filter((name) => bridges.has(name)).sort()
}

function lowestPort(ports: number[]): string {
  return ports.length === 0 ? "" : String(Math.min(...ports))
}

// parsePort returns the whole number 1 to 65535 the text spells in digits, or null.
function parsePort(text: string): number | null {
  const t = text.trim()
  if (!/^\d+$/.test(t)) return null
  const n = Number(t)
  return n >= 1 && n <= 65535 ? n : null
}

export interface ExposeTunnelFormProps {
  container: ContainerRef
  tunnels: { id: string; name: string }[]
  endpoints: string[]
  networks: DockerNetwork[]
  ports: number[]
  onDone: () => void
}

export function ExposeTunnelForm({ container, tunnels, endpoints, networks, ports, onDone }: ExposeTunnelFormProps) {
  const base = useId()
  const name = container.name.replace(/^\//, "")
  const supported = targetNetworks(endpoints, networks)
  const [pickedTunnel, setPickedTunnel] = useState("")
  const [pickedNetwork, setPickedNetwork] = useState("")
  const [portText, setPortText] = useState(() => lowestPort(ports))
  const [hostnameText, setHostnameText] = useState("")
  const [pending, setPending] = useState<Pending | null>(null)

  const tunnel = tunnels.some((t) => t.id === pickedTunnel) ? pickedTunnel : (tunnels[0]?.id ?? "")
  // Without a supported network the plan still goes to the controller, so
  // its own refusal shows in the preview.
  const network = supported.includes(pickedNetwork) ? pickedNetwork : (supported[0] ?? [...endpoints].sort()[0] ?? "")
  const port = parsePort(portText)
  const hostname = hostnameText.trim().toLowerCase()
  const ready = tunnel !== "" && port !== null && hostname !== ""

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!ready) return
    setPending({ title: `Expose ${name} at ${hostname}`, input: { hostId: container.hostId, tunnel, container: name, port, network, hostname } })
  }

  return (
    <>
      <form aria-label="Expose container via tunnel" className="flex flex-col gap-4" onSubmit={submit}>
        <fieldset className="flex flex-col gap-2">
          <legend className="mb-2 text-sm font-medium">Tunnel</legend>
          <RadioGroup value={tunnel} onValueChange={setPickedTunnel} aria-label="Tunnel">
            {tunnels.map((t, i) => (
              <div key={t.id} className="flex items-center gap-2">
                <RadioGroupItem id={`${base}-tunnel${i}`} value={t.id} />
                <Label htmlFor={`${base}-tunnel${i}`} className="font-mono">
                  {tunnelLabel(t.id, tunnels)}
                </Label>
              </div>
            ))}
          </RadioGroup>
        </fieldset>
        {supported.length === 0 ? (
          <Alert variant="warning" role="status">
            <AlertDescription>{UNSUPPORTED}</AlertDescription>
          </Alert>
        ) : (
          <fieldset className="flex flex-col gap-2">
            <legend className="mb-2 text-sm font-medium">Network</legend>
            <RadioGroup value={network} onValueChange={setPickedNetwork} aria-label="Network">
              {supported.map((n, i) => (
                <div key={n} className="flex items-center gap-2">
                  <RadioGroupItem id={`${base}-network${i}`} value={n} />
                  <Label htmlFor={`${base}-network${i}`}>{n}</Label>
                </div>
              ))}
            </RadioGroup>
          </fieldset>
        )}
        <div className="flex flex-col gap-2">
          <Label htmlFor={`${base}-port`}>Port</Label>
          <Input
            id={`${base}-port`}
            autoComplete="off"
            value={portText}
            aria-invalid={portText !== "" && port === null ? true : undefined}
            onChange={(e) => setPortText(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-2">
          <Label htmlFor={`${base}-hostname`}>Hostname</Label>
          <Input id={`${base}-hostname`} autoComplete="off" value={hostnameText} onChange={(e) => setHostnameText(e.target.value)} />
        </div>
        <div>
          <Button type="submit" disabled={!ready}>
            Plan
          </Button>
        </div>
      </form>
      <CloudflareChange
        action="cloudflare.expose.container"
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
