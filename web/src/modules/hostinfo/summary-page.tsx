import type { ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Card, CardContent } from "darkraise-ui/components/card"
import { Activity, Clock, Cpu, HardDrive, MemoryStick } from "lucide-react"
import { Gauge } from "@/components/visual/gauge"
import { StatTile } from "@/components/visual/stat-tile"
import { Spinner } from "darkraise-ui/components/spinner"
import { errorMessage, readResource } from "@/api/client"
import { formatTime, formatUptime } from "@/lib/format"

import type { Summary } from "./types"

export type { Summary } from "./types"

const gib = (bytes: number) => Math.round((bytes / 2 ** 30) * 10) / 10

function GaugeCard({ title, icon: Icon, children }: { title: string; icon: typeof Cpu; children: ReactNode }) {
  return (
    <Card role="region" aria-label={title}>
      <CardContent className="flex flex-col items-center gap-2 p-4">
        <span className="text-muted-foreground flex items-center gap-2 self-start text-sm font-medium">
          <Icon aria-hidden className="size-4" />
          {title}
        </span>
        {children}
      </CardContent>
    </Card>
  )
}

function Fact({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <Badge variant="outline" aria-label={`${label}: ${value}`} className={mono ? "font-mono" : undefined}>
      {value}
    </Badge>
  )
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
  const s = summary.data
  return (
    <div className="flex flex-col gap-4">
      <div aria-label="Facts" role="group" className="flex flex-wrap gap-1">
        <Fact label="Hostname" value={s.hostname} mono />
        <Fact label="Operating system" value={s.os.prettyName} />
        <Fact label="Kernel" value={s.kernel} mono />
        <Fact label="Architecture" value={s.arch} mono />
        <Fact label="Darkpanel version" value={s.darkpanelVersion} mono />
        <Fact label="Booted" value={formatTime(s.bootTime)} />
      </div>
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
        <GaugeCard title="Memory" icon={MemoryStick}>
          <Gauge value={gib(s.memory.totalBytes - s.memory.availableBytes)} max={gib(s.memory.totalBytes)} label="Memory" unit="GiB" />
        </GaugeCard>
        <GaugeCard title="Root filesystem" icon={HardDrive}>
          <Gauge value={gib(s.rootFs.totalBytes - s.rootFs.availableBytes)} max={gib(s.rootFs.totalBytes)} label="Disk" unit="GiB" />
        </GaugeCard>
        <GaugeCard title="Swap" icon={Activity}>
          {s.swap.totalBytes === 0 ? (
            <p className="text-muted-foreground text-sm">No swap</p>
          ) : (
            <Gauge value={gib(s.swap.totalBytes - s.swap.freeBytes)} max={gib(s.swap.totalBytes)} label="Swap" unit="GiB" />
          )}
        </GaugeCard>
        <div className="flex flex-col gap-4">
          <StatTile icon={Clock} figure={formatUptime(s.uptimeSeconds)} caption="Uptime" />
          <StatTile icon={Cpu} figure={[s.load.one, s.load.five, s.load.fifteen].map((n) => n.toFixed(2)).join(" / ")} caption="Load average">
            <span className="text-muted-foreground text-xs">{`${s.cpu.model} (${s.cpu.logicalCount} logical ${s.cpu.logicalCount === 1 ? "CPU" : "CPUs"})`}</span>
          </StatTile>
        </div>
      </div>
    </div>
  )
}
