import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Card, CardContent } from "darkraise-ui/components/card"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readResource } from "@/api/client"
import { formatBytes, formatDuration, formatTime } from "@/lib/format"

// The output of the hostinfo.summary resource (schemas/hostinfo.summary).
export interface Summary {
  hostname: string
  os: { id: string; versionId: string; prettyName: string }
  kernel: string
  arch: string
  uptimeSeconds: number
  bootTime: string
  cpu: { model: string; logicalCount: number }
  memory: { totalBytes: number; availableBytes: number }
  swap: { totalBytes: number; freeBytes: number }
  load: { one: number; five: number; fifteen: number }
  rootFs: { totalBytes: number; availableBytes: number }
  darkpanelVersion: string
}

interface Row {
  label: string
  value: string
  mono?: boolean
}

function rows(s: Summary): Row[] {
  return [
    { label: "Hostname", value: s.hostname, mono: true },
    { label: "Operating system", value: s.os.prettyName },
    { label: "Kernel", value: s.kernel, mono: true },
    { label: "Architecture", value: s.arch, mono: true },
    { label: "Uptime", value: formatDuration(s.uptimeSeconds) },
    { label: "Booted", value: formatTime(s.bootTime) },
    { label: "CPU", value: `${s.cpu.model} (${s.cpu.logicalCount} logical ${s.cpu.logicalCount === 1 ? "CPU" : "CPUs"})` },
    { label: "Load average", value: [s.load.one, s.load.five, s.load.fifteen].map((n) => n.toFixed(2)).join(" / ") },
    { label: "Memory", value: `${formatBytes(s.memory.availableBytes)} available of ${formatBytes(s.memory.totalBytes)}` },
    {
      label: "Swap",
      value: s.swap.totalBytes === 0 ? "None" : `${formatBytes(s.swap.freeBytes)} free of ${formatBytes(s.swap.totalBytes)}`,
    },
    { label: "Root filesystem", value: `${formatBytes(s.rootFs.availableBytes)} available of ${formatBytes(s.rootFs.totalBytes)}` },
    { label: "Darkpanel version", value: s.darkpanelVersion, mono: true },
  ]
}

export function SummaryPage({ hostId }: { hostId: string }) {
  const summary = useQuery({
    queryKey: ["resource", hostId, "hostinfo.summary"],
    queryFn: () => readResource<Summary>(hostId, "hostinfo", "summary"),
  })
  if (summary.isPending) return <Spinner aria-label="Loading host information" />
  if (summary.isError) {
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription>{errorMessage(summary.error)}</AlertDescription>
      </Alert>
    )
  }
  return (
    <Card>
      <CardContent>
        <dl className="grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-[max-content_1fr]">
          {rows(summary.data).map((row) => (
            <div key={row.label} className="contents">
              <dt className="text-muted-foreground">{row.label}</dt>
              <dd className={row.mono === true ? "font-mono" : undefined}>{row.value}</dd>
            </div>
          ))}
        </dl>
      </CardContent>
    </Card>
  )
}
