import { useState } from "react"
import { formatBytes } from "@/lib/format"
import { StreamEndNotice } from "./stream-end"
import { streamUrl, useStream } from "./stream"
import { DOCKER_MODULE } from "./types"
import type { StatsEvent } from "./types"

function memoryText(sample: StatsEvent): string {
  if (!(sample.memLimit > 0)) return formatBytes(sample.memUsage)
  return `${formatBytes(sample.memUsage)} of ${formatBytes(sample.memLimit)} (${((sample.memUsage / sample.memLimit) * 100).toFixed(1)} %)`
}

function Row({ label, children }: { label: string; children: string | number }) {
  return (
    <div className="flex gap-2">
      <dt className="w-24 text-muted-foreground">{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

function StatsFeed({ hostId, containerId, onReconnect }: { hostId: string; containerId: string; onReconnect: () => void }) {
  const { events, end } = useStream<StatsEvent>(streamUrl(hostId, DOCKER_MODULE, "stats", { container: containerId }), 1)
  const sample = events[events.length - 1]

  return (
    <div className="flex flex-col gap-3">
      {sample === undefined ? (
        end === null && <p className="text-sm text-muted-foreground">Waiting for the first sample</p>
      ) : (
        <dl aria-label="Container usage" className="flex flex-col gap-1 text-sm">
          <Row label="CPU">{`${sample.cpuPercent.toFixed(1)} %`}</Row>
          <Row label="Memory">{memoryText(sample)}</Row>
          <Row label="Received">{formatBytes(sample.netRx)}</Row>
          <Row label="Sent">{formatBytes(sample.netTx)}</Row>
          <Row label="Processes">{sample.pids}</Row>
        </dl>
      )}
      {end !== null && <StreamEndNotice end={end} done="The stats stream ended." onReconnect={onReconnect} />}
    </div>
  )
}

export function StatsPanel({ hostId, containerId }: { hostId: string; containerId: string }) {
  const [attempt, setAttempt] = useState(0)
  return <StatsFeed key={attempt} hostId={hostId} containerId={containerId} onReconnect={() => setAttempt((a) => a + 1)} />
}
