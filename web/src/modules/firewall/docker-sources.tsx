import { useState, type FormEvent, type ReactNode } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { Textarea } from "darkraise-ui/components/textarea"
import { FirewallChange } from "./firewall-change"
import type { DockerProto, DockerStatus, Exemption } from "./types"

const PROTOS: DockerProto[] = ["tcp", "udp"]

interface SourcesDraft {
  base: string
  text: string
}

interface AddDraft {
  inInterface: string
  origDestination: string
  origPort: string
  destination: string
  port: string
  proto: DockerProto
}

const EMPTY_ADD: AddDraft = { inInterface: "", origDestination: "", origPort: "", destination: "", port: "", proto: "tcp" }

interface Pending {
  title: string
  input: Record<string, unknown>
}

// linesOf is the ranges a textarea holds: its lines, trimmed, blanks dropped.
function linesOf(text: string): string[] {
  return text.split("\n").map((l) => l.trim()).filter((l) => l !== "")
}

function validPort(text: string): boolean {
  if (!/^\d+$/.test(text)) return false
  const n = Number(text)
  return n >= 1 && n <= 65535
}

function anyOf(value: string | number | undefined): string {
  return value === undefined ? "any" : String(value)
}

// ExemptionTable lists exemptions; actions renders the last cell of a row.
function ExemptionTable({ label, rows, actions }: {
  label: string
  rows: Exemption[]
  actions: (index: number) => ReactNode
}) {
  return (
    <Table aria-label={label}>
      <TableHeader>
        <TableRow>
          <TableHead>Interface</TableHead>
          <TableHead>Original destination</TableHead>
          <TableHead>Original port</TableHead>
          <TableHead>Destination</TableHead>
          <TableHead>Port</TableHead>
          <TableHead>Protocol</TableHead>
          <TableHead>
            <span className="sr-only">Actions</span>
          </TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {rows.map((e, i) => (
          <TableRow key={i}>
            <TableCell>{anyOf(e.inInterface)}</TableCell>
            <TableCell className="font-mono">{e.origDestination}</TableCell>
            <TableCell>{anyOf(e.origPort)}</TableCell>
            <TableCell className="font-mono">{e.destination}</TableCell>
            <TableCell>{anyOf(e.port)}</TableCell>
            <TableCell>{e.proto}</TableCell>
            <TableCell>{actions(i)}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

function Field({ id, label, value, onChange }: { id: string; label: string; value: string; onChange: (value: string) => void }) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} autoComplete="off" onChange={(e) => onChange(e.target.value)} />
    </div>
  )
}

// DockerSources edits the Docker integration's private source ranges and
// forward exemptions. Each edit plans firewall.docker.set-sources with the
// one list it changes.
export function DockerSources({ hostId, docker }: { hostId: string; docker: DockerStatus }) {
  const sources = docker.settings.privateSources ?? []
  const exemptions = docker.settings.forwardExemptions ?? []
  const proposed = docker.foreignForwards ?? []

  const [sourcesDraft, setSourcesDraft] = useState<SourcesDraft | null>(null)
  const [add, setAdd] = useState<AddDraft>(EMPTY_ADD)
  const [problem, setProblem] = useState("")
  const [pending, setPending] = useState<Pending | null>(null)

  const current = sources.join("\n")
  const text = sourcesDraft !== null && sourcesDraft.base === current ? sourcesDraft.text : current
  const ranges = linesOf(text)
  const unchanged = ranges.length === sources.length && ranges.every((r, i) => r === sources[i])

  function setField<K extends keyof AddDraft>(key: K, value: AddDraft[K]) {
    setAdd((d) => ({ ...d, [key]: value }))
  }

  function plan(title: string, list: Exemption[]) {
    setPending({ title, input: { forwardExemptions: list } })
  }

  function submitSources(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending({ title: "Set the private sources", input: { privateSources: ranges } })
  }

  function submitAdd(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const origDestination = add.origDestination.trim()
    const destination = add.destination.trim()
    if (origDestination === "" || destination === "") {
      setProblem("Enter the original destination and the destination.")
      return
    }
    const origPort = add.origPort.trim()
    const port = add.port.trim()
    if ((origPort !== "" && !validPort(origPort)) || (port !== "" && !validPort(port))) {
      setProblem("Ports are whole numbers from 1 to 65535.")
      return
    }
    setProblem("")
    const exemption: Exemption = { origDestination, destination, proto: add.proto }
    if (add.inInterface.trim() !== "") exemption.inInterface = add.inInterface.trim()
    if (origPort !== "") exemption.origPort = Number(origPort)
    if (port !== "") exemption.port = Number(port)
    plan("Add a forward exemption", [...exemptions, exemption])
  }

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>Private sources</CardTitle>
          <CardDescription>Traffic from these ranges reaches every published port, whatever the container rules allow.</CardDescription>
        </CardHeader>
        <CardContent>
          <form aria-label="Private sources" className="flex flex-col gap-4" onSubmit={submitSources}>
            <div className="flex flex-col gap-2">
              <Label htmlFor="ds-sources">Private source ranges</Label>
              <Textarea id="ds-sources" rows={6} className="font-mono" value={text} onChange={(e) => setSourcesDraft({ base: current, text: e.target.value })} />
              <p className="text-sm text-muted-foreground">One CIDR per line.</p>
            </div>
            <div>
              <Button type="submit" disabled={unchanged}>Set private sources</Button>
            </div>
          </form>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>Forward exemptions</CardTitle>
          <CardDescription>Port forwards that are not Docker&apos;s, which the block would otherwise deny.</CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-6">
          {exemptions.length === 0 ? (
            <p>No forward exemptions.</p>
          ) : (
            <ExemptionTable label="Forward exemptions" rows={exemptions} actions={(i) => (
              <Button variant="outline" size="sm" aria-label={`Remove exemption ${i + 1}`}
                onClick={() => plan("Remove a forward exemption", exemptions.filter((_, j) => j !== i))}>
                Remove
              </Button>
            )} />
          )}
          {proposed.length > 0 && (
            <ExemptionTable label="Proposed forward exemptions" rows={proposed} actions={(i) => (
              <Button variant="outline" size="sm" aria-label={`Add proposed exemption ${i + 1}`}
                onClick={() => plan("Add a forward exemption", [...exemptions, proposed[i] as Exemption])}>
                Add
              </Button>
            )} />
          )}
          <form aria-label="Add forward exemption" className="flex flex-col gap-4" onSubmit={submitAdd}>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field id="ds-in-interface" label="In interface (optional)" value={add.inInterface} onChange={(v) => setField("inInterface", v)} />
              <Field id="ds-orig-destination" label="Original destination" value={add.origDestination} onChange={(v) => setField("origDestination", v)} />
              <Field id="ds-orig-port" label="Original port (optional)" value={add.origPort} onChange={(v) => setField("origPort", v)} />
              <Field id="ds-destination" label="Destination" value={add.destination} onChange={(v) => setField("destination", v)} />
              <Field id="ds-port" label="Destination port (optional)" value={add.port} onChange={(v) => setField("port", v)} />
            </div>
            <fieldset className="flex flex-col gap-2">
              <legend className="text-sm font-medium">Protocol</legend>
              <RadioGroup value={add.proto} onValueChange={(v) => setField("proto", v as DockerProto)} aria-label="Protocol" className="flex flex-wrap gap-4">
                {PROTOS.map((p) => (
                  <div key={p} className="flex items-center gap-2">
                    <RadioGroupItem id={`ds-proto-${p}`} value={p} />
                    <Label htmlFor={`ds-proto-${p}`}>{p}</Label>
                  </div>
                ))}
              </RadioGroup>
            </fieldset>
            {problem !== "" && <p role="alert" className="text-sm text-destructive">{problem}</p>}
            <div>
              <Button type="submit">Add exemption</Button>
            </div>
          </form>
        </CardContent>
      </Card>
      <FirewallChange
        hostId={hostId}
        action="firewall.docker.set-sources"
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => {
          setPending(null)
          setSourcesDraft(null)
          setAdd(EMPTY_ADD)
        }}
      />
    </>
  )
}
