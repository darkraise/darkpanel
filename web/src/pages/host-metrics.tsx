import { useState } from "react"
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { SegmentGroup, SegmentGroupIndicator, SegmentGroupItem } from "darkraise-ui/components/segment-group"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { HOSTS_QUERY_KEY, errorMessage, readAction, readResource } from "@/api/client"
import type { Host, MetricsHistory, MetricsRange } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { MetricChart } from "@/components/visual/metric-chart"
import { formatTime } from "@/lib/format"
import { METRICS_RANGES, RANGE_TEXT, charts, historyKey, pollReason } from "@/lib/metrics"
import { applied } from "@/lib/users"
import type { Summary } from "@/modules/hostinfo/summary-page"
import { useCanOnHost } from "@/session/can"

// New minutes arrive once a minute, through no events-stream key.
const REFRESH_MS = 60_000

function readHistory(hostId: string, range: MetricsRange): Promise<MetricsHistory> {
  return readAction<MetricsHistory>("metrics.history.get", 1, { hostId, range })
}

function MetricsOff({ host }: { host: Host }) {
  const admin = useCanOnHost("admin")
  const client = useQueryClient()
  const [enabling, setEnabling] = useState(false)
  const present = host.modules.some((m) => m.id === "metrics")
  return (
    <>
      <Alert role="status">
        <AlertDescription className="flex flex-wrap items-center gap-2">
          Metrics are off on this host
          {admin && present && (
            <Button size="sm" onClick={() => setEnabling(true)}>
              Enable
            </Button>
          )}
        </AlertDescription>
      </Alert>
      {enabling && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : setEnabling(false))}
          title={`Enable metrics on ${host.name}`}
          target={{ name: "core.module.enable", version: 1 }}
          input={{ hostId: host.id, moduleId: "metrics" }}
          onApplied={(change) => {
            if (applied(change)) void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
          }}
        />
      )}
    </>
  )
}

function Charts({ host, range }: { host: Host; range: MetricsRange }) {
  const history = useQuery({ queryKey: historyKey(host.id, range), queryFn: () => readHistory(host.id, range), refetchInterval: REFRESH_MS, placeholderData: keepPreviousData })
  // The overview's figure row reads the same summary; its CPU count turns
  // the raw load into load per CPU, as the load alert rules count it.
  const info = useQuery({
    queryKey: ["resource", host.id, "hostinfo.summary"],
    queryFn: () => readResource<Summary>(host.id, "hostinfo", "summary"),
    enabled: host.online,
  })
  if (history.isPending)
    return (
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2" aria-label="Loading the metrics">
        {Array.from({ length: 5 }, (_, i) => (
          <Skeleton key={i} className="h-60 w-full" />
        ))}
      </div>
    )
  if (history.isError)
    return (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't load metrics: ${errorMessage(history.error)}`}</span>
          <Button size="sm" variant="outline" onClick={() => void history.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  const h = history.data
  const status = h.status
  // While another range loads, the previous one stays on screen, labelled as
  // what it is.
  const shown = history.isPlaceholderData ? h.range : range
  const banners = []
  if (history.isPlaceholderData) banners.push(<p key="loading" role="status" className="text-sm text-muted-foreground">{`Loading the ${RANGE_TEXT[range]}…`}</p>)
  if (!host.online) banners.push(<Alert key="offline" variant="warning" role="status"><AlertDescription>{`Offline since ${formatTime(host.lastSeenAt)}`}</AlertDescription></Alert>)
  if (status !== null && status.lastErrorCode !== null)
    banners.push(
      <Alert key="poll" variant="destructive">
        <AlertDescription>{status.lastOk === null ? `No samples yet: ${pollReason(status)}` : `No new samples since ${formatTime(status.lastOk)}: ${pollReason(status)}`}</AlertDescription>
      </Alert>,
    )
  const collecting = h.points.length === 0 && (status === null || status.lastOk === null) && status?.lastErrorCode == null && host.online
  if (collecting) banners.push(<Alert key="collecting" role="status"><AlertDescription>Collecting — the first samples appear within 2 minutes</AlertDescription></Alert>)
  if (h.points.length === 0)
    return (
      <>
        {banners}
        {!collecting && <p className="text-sm text-muted-foreground">{`No samples in the ${RANGE_TEXT[shown]}.`}</p>}
      </>
    )
  const cpus = info.data?.cpu.logicalCount ?? null
  const c = charts(h, cpus)
  const daily = h.resolution === "1h" || shown === "7d"
  const common = { daily, rangeText: RANGE_TEXT[shown] }
  return (
    <>
      {banners}
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2" aria-busy={history.isPlaceholderData}>
        <MetricChart title="CPU (%)" unit=" %" data={c.cpu} area {...common} />
        <MetricChart title="Memory (GiB)" unit=" GiB" data={c.memory} area {...common} />
        <MetricChart title="Disk per mount (%)" unit=" %" data={c.disk} {...common} />
        <MetricChart title="Network per interface (MB/s)" unit=" MB/s" data={c.network} {...common} />
        <MetricChart title={c.perCpu ? "Load per CPU" : "Load"} unit="" data={c.load} {...common} />
      </div>
    </>
  )
}

// MetricsSection is the host overview's history: a range control and five
// charts, or why there is nothing to draw.
export function MetricsSection({ host }: { host: Host }) {
  const [range, setRange] = useState<MetricsRange>("1h")
  const on = host.modules.some((m) => m.id === "metrics" && m.enabled)
  return (
    <section aria-labelledby="host-metrics" className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="host-metrics" className="text-lg font-semibold">
          Metrics
        </h2>
        {on && (
          <SegmentGroup value={range} onValueChange={(r) => setRange(r as MetricsRange)} aria-label="Range" variant="outline">
            <SegmentGroupIndicator />
            {METRICS_RANGES.map((r) => (
              <SegmentGroupItem key={r} value={r}>
                {r}
              </SegmentGroupItem>
            ))}
          </SegmentGroup>
        )}
      </div>
      {on ? <Charts host={host} range={range} /> : <MetricsOff host={host} />}
    </section>
  )
}
