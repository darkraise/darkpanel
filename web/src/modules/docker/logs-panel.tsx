import { Checkbox } from "darkraise-ui/components/checkbox"
import { Label } from "darkraise-ui/components/label"
import { useState } from "react"
import { StreamEndNotice } from "./stream-end"
import { streamUrl, useStream } from "./stream"
import { DOCKER_MODULE } from "./types"
import type { LogEvent } from "./types"

const TAILS = ["50", "200", "1000", "all"]

function lineText(text: string): string {
  if (text.endsWith("\r\n")) return text.slice(0, -2)
  return text.endsWith("\n") ? text.slice(0, -1) : text
}

function LogFeed({
  hostId,
  containerId,
  follow,
  tail,
  onReconnect,
}: {
  hostId: string
  containerId: string
  follow: boolean
  tail: string
  onReconnect: () => void
}) {
  const params: Record<string, string> = { container: containerId, tail }
  if (follow) params.follow = "true"
  const { events, end } = useStream<LogEvent>(streamUrl(hostId, DOCKER_MODULE, "logs", params))

  return (
    <>
      <div
        role="log"
        aria-label="Container logs"
        aria-live="off"
        tabIndex={0}
        className="max-h-96 overflow-auto rounded-md bg-muted p-3 font-mono text-xs"
      >
        {events.map((event, i) => (
          <div key={i} data-stream={event.stream} className={event.stream === "stderr" ? "whitespace-pre-wrap text-destructive" : "whitespace-pre-wrap"}>
            {lineText(event.text)}
          </div>
        ))}
      </div>
      {events.length === 0 && end === null && <p className="text-sm text-muted-foreground">Waiting for output</p>}
      {events.length === 0 && end !== null && <p className="text-sm text-muted-foreground">No log output.</p>}
      {end !== null && <StreamEndNotice end={end} done="The log ended." onReconnect={onReconnect} />}
    </>
  )
}

export function LogsPanel({ hostId, containerId }: { hostId: string; containerId: string }) {
  const [follow, setFollow] = useState(true)
  const [tail, setTail] = useState("200")
  const [attempt, setAttempt] = useState(0)

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-4">
        <div className="flex items-center gap-2">
          <Checkbox id="logs-follow" checked={follow} onCheckedChange={(checked) => setFollow(checked === true)} />
          <Label htmlFor="logs-follow">Follow</Label>
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor="logs-tail">Tail</Label>
          <select
            id="logs-tail"
            value={tail}
            onChange={(e) => setTail(e.target.value)}
            className="rounded-md border border-input bg-background px-2 py-1 text-sm"
          >
            {TAILS.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </div>
      </div>
      <LogFeed
        key={attempt}
        hostId={hostId}
        containerId={containerId}
        follow={follow}
        tail={tail}
        onReconnect={() => setAttempt((a) => a + 1)}
      />
    </div>
  )
}
