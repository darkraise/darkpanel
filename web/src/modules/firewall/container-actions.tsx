import { useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readResource } from "@/api/client"
import type { ContainerRef } from "@/modules/types"
import { FirewallChange } from "./firewall-change"
import { dockerQueryKey, FIREWALL_MODULE, type Binding, type DockerStatus } from "./types"

type Kind = "allow" | "deny"

interface Candidate {
  key: string
  label: string
  binding: Binding
  allowed: boolean
}

interface Pending {
  title: string
  input: Record<string, unknown>
}

const ANY_ADDRESS = new Set(["", "0.0.0.0", "::"])

function bindingKey(b: Binding): string {
  return `${b.hostAddr ?? ""}|${b.hostPort}|${b.proto}`
}

function bindingLabel(b: Binding): string {
  const addr = b.hostAddr === undefined ? "all" : b.hostAddr.includes(":") ? `[${b.hostAddr}]` : b.hostAddr
  return `${addr}:${b.hostPort}/${b.proto}`
}

// candidatesOf lists the bindings of one container: those the integration
// stores for it, then each published port it has no binding for yet.
function candidatesOf(docker: DockerStatus, container: ContainerRef, name: string): Candidate[] {
  const found = new Map<string, Candidate>()
  function add(binding: Binding, allowed: boolean) {
    const key = bindingKey(binding)
    if (!found.has(key)) found.set(key, { key, label: bindingLabel(binding), binding, allowed })
  }
  for (const d of docker.bindings) {
    if (d.containers.includes(name)) add(d.binding, d.allowed.length > 0)
  }
  for (const port of container.ports) {
    for (const published of port.bindings) {
      const binding: Binding = { selector: name, hostPort: published.hostPort, proto: port.protocol }
      if (!ANY_ADDRESS.has(published.hostIp)) binding.hostAddr = published.hostIp
      add(binding, false)
    }
  }
  return [...found.values()]
}

function disabledReason(docker: DockerStatus, kind: Kind, name: string, offered: Candidate[]): string | null {
  if (!docker.available) return docker.reason ?? "Docker is unavailable on this host."
  if (!docker.supported) return `The Docker integration is not supported: ${(docker.unsupported ?? []).join("; ")}`
  if (offered.length === 0) return kind === "allow" ? `${name} publishes no ports.` : `${name} has no allowed binding. Allow one first.`
  return null
}

function ContainerRuleForm({ kind, container, onDone }: { kind: Kind; container: ContainerRef; onDone: () => void }) {
  const name = container.name.replace(/^\//, "")
  const [picked, setPicked] = useState("")
  const [from, setFrom] = useState("")
  const [pending, setPending] = useState<Pending | null>(null)
  const docker = useQuery({
    queryKey: dockerQueryKey(container.hostId),
    queryFn: () => readResource<DockerStatus>(container.hostId, FIREWALL_MODULE, "docker"),
  })
  if (docker.isPending) return <Spinner aria-label="Loading the Docker integration" />
  if (docker.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(docker.error)}</AlertDescription>
      </Alert>
    )
  }

  const offered = candidatesOf(docker.data, container, name).filter((c) => kind === "allow" || c.allowed)
  const selected = offered.find((c) => c.key === picked) ?? offered[0]
  const reason = disabledReason(docker.data, kind, name, offered)
  const source = from.trim()
  const verb = kind === "allow" ? "Allow" : "Deny"
  const fieldId = `ctr-${kind}-from`

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (reason !== null || selected === undefined) return
    const who = source === "" ? "any source" : source
    setPending({
      title: `${verb} ${who} to ${name} on ${selected.label}`,
      input: source === "" ? { binding: selected.binding } : { binding: selected.binding, from: source },
    })
  }

  return (
    <>
      <form aria-label={verb} className="flex flex-col gap-3" onSubmit={submit}>
        {reason !== null && (
          <Alert variant="warning" role="status">
            <AlertDescription>{reason}</AlertDescription>
          </Alert>
        )}
        <fieldset className="flex flex-col gap-2" disabled={reason !== null}>
          <legend className="text-sm font-medium">Binding</legend>
          <RadioGroup value={selected?.key ?? ""} onValueChange={setPicked} aria-label="Binding" className="flex flex-wrap gap-4">
            {offered.map((c) => (
              <div key={c.key} className="flex items-center gap-2">
                <RadioGroupItem id={`ctr-${kind}-${c.key}`} value={c.key} />
                <Label htmlFor={`ctr-${kind}-${c.key}`}>{c.label}</Label>
              </div>
            ))}
          </RadioGroup>
        </fieldset>
        <div className="flex flex-col gap-2">
          <Label htmlFor={fieldId}>From</Label>
          <Input
            id={fieldId}
            value={from}
            autoComplete="off"
            disabled={reason !== null}
            placeholder={kind === "allow" ? "203.0.113.0/24, empty for any source" : "203.0.113.0/24"}
            onChange={(e) => setFrom(e.target.value)}
          />
        </div>
        <div>
          <Button type="submit" disabled={reason !== null || selected === undefined || (kind === "deny" && source === "")}>
            Plan
          </Button>
        </div>
      </form>
      <FirewallChange
        hostId={container.hostId}
        action={`firewall.container.${kind}`}
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

// AllowAction opens the change that allows a source to one binding of a
// container.
export function AllowAction({ container, onDone }: { container: ContainerRef; onDone: () => void }) {
  return <ContainerRuleForm kind="allow" container={container} onDone={onDone} />
}

// DenyAction opens the change that denies a source to one allowed binding of
// a container.
export function DenyAction({ container, onDone }: { container: ContainerRef; onDone: () => void }) {
  return <ContainerRuleForm kind="deny" container={container} onDone={onDone} />
}
