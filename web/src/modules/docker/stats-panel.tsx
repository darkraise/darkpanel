import { useState } from "react"
import { ArrowDownToLine, ArrowUpFromLine, Cpu, ListTree, MemoryStick } from "lucide-react"
import { StatTile } from "@/components/visual/stat-tile"
import { formatBytes } from "@/lib/format"
import { StreamEndNotice } from "./stream-end"
import { streamUrl, useStream } from "./stream"
import { DOCKER_MODULE } from "./types"
import type { StatsEvent } from "./types"

function memoryLimit(sample: StatsEvent): string | null {
  if (!(sample.memLimit > 0)) return null
  return `of ${formatBytes(sample.memLimit)} (${((sample.memUsage / sample.memLimit) * 100).toFixed(1)} %)`
}

function StatsFeed({ hostId, containerId, onReconnect }: { hostId: string; containerId: string; onReconnect: () => void }) {
  const { events, end } = useStream<StatsEvent>(streamUrl(hostId, DOCKER_MODULE, "stats", { container: containerId }), 1)
  const sample = events[events.length - 1]

  return (
    <div className="flex flex-col gap-3">
      {sample === undefined ? (
        end === null && <p className="text-sm text-muted-foreground">Waiting for the first sample</p>
      ) : (
        <div role="group" aria-label="Container usage" className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-4">
          <StatTile icon={Cpu} figure={`${sample.cpuPercent.toFixed(1)} %`} caption="CPU" />
          <StatTile icon={MemoryStick} figure={formatBytes(sample.memUsage)} caption="Memory">
            {memoryLimit(sample) !== null && <div className="text-muted-foreground text-xs">{memoryLimit(sample)}</div>}
          </StatTile>
          <StatTile icon={ArrowDownToLine} figure={formatBytes(sample.netRx)} caption="Received" />
          <StatTile icon={ArrowUpFromLine} figure={formatBytes(sample.netTx)} caption="Sent" />
          <StatTile icon={ListTree} figure={sample.pids} caption="Processes" />
        </div>
      )}
      {end !== null && <StreamEndNotice end={end} done="The stats stream ended." onReconnect={onReconnect} />}
    </div>
  )
}

export function StatsPanel({ hostId, containerId }: { hostId: string; containerId: string }) {
  const [attempt, setAttempt] = useState(0)
  return <StatsFeed key={attempt} hostId={hostId} containerId={containerId} onReconnect={() => setAttempt((a) => a + 1)} />
}
