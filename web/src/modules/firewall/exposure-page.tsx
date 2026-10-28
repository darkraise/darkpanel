import { useState } from "react"
import { Link, useParams } from "react-router"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { Spinner } from "darkraise-ui/components/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readResource } from "@/api/client"
import { exposureQueryKey, FIREWALL_MODULE, type ExposureRow, type ExposureVerdict, type ExposureView } from "./types"

type VerdictBadge = "destructive" | "default" | "secondary" | "outline"

const BADGES: Record<ExposureVerdict["verdict"], VerdictBadge> = {
  open: "destructive",
  partial: "default",
  unknown: "secondary",
  closed: "outline",
  filtered: "outline",
}

const INTERNET_REACHES: ExposureVerdict["verdict"][] = ["open", "partial", "unknown"]

// Choice is one filter as a radio group whose first option is "all".
function Choice({ name, label, allLabel, options, value, onChange }: {
  name: string
  label: string
  allLabel: string
  options: string[]
  value: string
  onChange: (value: string) => void
}) {
  const items = [{ value: "all", text: allLabel }, ...options.map((o) => ({ value: o, text: o }))]
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="text-sm font-medium">{label}</legend>
      <RadioGroup value={value} onValueChange={onChange} aria-label={label} className="flex flex-wrap gap-4">
        {items.map((o) => (
          <div key={o.value} className="flex items-center gap-2">
            <RadioGroupItem id={`exp-${name}-${o.value}`} value={o.value} />
            <Label htmlFor={`exp-${name}-${o.value}`}>{o.text}</Label>
          </div>
        ))}
      </RadioGroup>
    </fieldset>
  )
}

function listener(row: ExposureRow): string {
  if (row.container !== undefined) {
    return `container ${row.container.name}, port ${row.container.containerPort}${row.container.proxied ? " (docker-proxy)" : ""}`
  }
  return row.process !== undefined && row.process !== "" ? row.process : "—"
}

function detail(v: ExposureVerdict): string {
  const parts: string[] = []
  if (v.decidedBy !== undefined && v.decidedBy !== "") parts.push(`decided by ${v.decidedBy}`)
  for (const a of v.allowed ?? []) {
    let text = `allowed from ${a.sources.join(", ")}`
    if (a.ifaces !== undefined && a.ifaces.length > 0) text += ` on ${a.ifaces.join(", ")}`
    if (a.srcPorts !== undefined && a.srcPorts.length > 0) text += ` from source ports ${a.srcPorts.join(", ")}`
    parts.push(`${text} (${a.decidedBy})`)
  }
  if (v.note !== undefined && v.note !== "") parts.push(v.note)
  return parts.join("; ")
}

function Reachability({ row }: { row: ExposureRow }) {
  return (
    <ul aria-label={`Reachability of ${row.proto} ${row.addr} port ${row.port}`} className="flex flex-col gap-1">
      {row.classes.map((v) => {
        const label = v.class === "private" && v.range !== undefined && v.range !== "" ? `private ${v.range}` : v.class
        const text = detail(v)
        return (
          <li key={`${v.class}/${v.range ?? ""}`}>
            <span>
              {label}:{" "}
              <Badge variant={BADGES[v.verdict]} size="sm">{v.verdict}</Badge>
              {text !== "" && <span className="text-xs text-muted-foreground"> — {text}</span>}
            </span>
          </li>
        )
      })}
    </ul>
  )
}

function ExposureTable({ view }: { view: ExposureView }) {
  const [proto, setProto] = useState("all")
  const [family, setFamily] = useState("all")
  const [internetOnly, setInternetOnly] = useState(false)
  const notes = view.notes ?? []
  const noteList = notes.length > 0 && (
    <Alert variant="warning" role="status">
      <AlertDescription>
        <ul aria-label="Exposure notes">
          {notes.map((n) => <li key={n}>{n}</li>)}
        </ul>
      </AlertDescription>
    </Alert>
  )
  if (view.rows.length === 0) {
    return (
      <div className="flex flex-col gap-4">
        {noteList}
        <EmptyState title="No listening ports" description="Nothing on this host is listening for connections." />
      </div>
    )
  }
  const shown = view.rows.filter(
    (r) =>
      (proto === "all" || r.proto === proto) &&
      (family === "all" || r.family === family) &&
      (!internetOnly || r.classes.some((c) => c.class === "internet" && INTERNET_REACHES.includes(c.verdict))),
  )
  return (
    <div className="flex flex-col gap-4">
      {noteList}
      <div className="flex flex-wrap gap-6">
        <Choice name="proto" label="Protocol" allLabel="All protocols" options={["tcp", "udp"]} value={proto} onChange={setProto} />
        <Choice name="family" label="Family" allLabel="All families" options={["v4", "v6"]} value={family} onChange={setFamily} />
        <div className="flex items-center gap-2 self-end">
          <Checkbox id="exp-internet" checked={internetOnly} onCheckedChange={(checked) => setInternetOnly(checked === true)} />
          <Label htmlFor="exp-internet">Only ports the internet can reach</Label>
        </div>
      </div>
      {shown.length === 0 ? (
        <EmptyState title="No rows match the filters" description="Change the filters to see the other listening ports." />
      ) : (
        <Table aria-label="Exposure">
          <TableHeader>
            <TableRow>
              <TableHead>Protocol</TableHead>
              <TableHead>Address</TableHead>
              <TableHead>Port</TableHead>
              <TableHead>Family</TableHead>
              <TableHead>Listener</TableHead>
              <TableHead>Reachable from</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((r) => (
              <TableRow key={`${r.proto}/${r.addr}/${r.port}/${r.family}`}>
                <TableCell>{r.proto}</TableCell>
                <TableCell className="font-mono">{r.addr}</TableCell>
                <TableCell>{r.port}</TableCell>
                <TableCell>{r.family}</TableCell>
                <TableCell>{listener(r)}</TableCell>
                <TableCell><Reachability row={r} /></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  )
}

// ExposurePage is /hosts/:hostId/firewall/exposure: every listening port
// with its reachability from each source class. It changes nothing.
export function ExposurePage() {
  const { hostId = "" } = useParams()
  const exposure = useQuery({
    queryKey: exposureQueryKey(hostId),
    queryFn: () => readResource<ExposureView>(hostId, FIREWALL_MODULE, "exposure"),
  })
  let body
  if (exposure.isPending) body = <Spinner aria-label="Loading the exposure" />
  else if (exposure.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(exposure.error)}</AlertDescription>
      </Alert>
    )
  } else body = <ExposureTable view={exposure.data} />
  return (
    <div className="flex flex-col gap-4">
      <Link to=".." relative="path" className="text-sm underline-offset-4 hover:underline">
        Back to the firewall
      </Link>
      <Card>
        <CardHeader>
          <CardTitle>Exposure</CardTitle>
        </CardHeader>
        <CardContent>{body}</CardContent>
      </Card>
    </div>
  )
}
