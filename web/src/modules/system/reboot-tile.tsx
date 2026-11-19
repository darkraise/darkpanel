import { useEffect, useState } from "react"
import { ArrowRight, Power } from "lucide-react"
import { Button } from "darkraise-ui/components/button"
import type { Host } from "@/api/types"
import { InfoTip } from "@/components/visual/info-tip"
import type { Status } from "@/components/visual/status"
import { formatDuration, formatTime } from "@/lib/format"
import { SystemChange, type PendingChange } from "./system-change"
import { Tile, TileError, TileSkeleton } from "./tile"
import type { RebootOutput, RebootState } from "./types"

const REBOOT_ACTION = "system.host.reboot"
const NOT_STARTED_AFTER_MS = 2 * 60_000
const NOT_BACK_AFTER_MS = 10 * 60_000
// After this long an online host shows its plain reboot state again.
const SETTLED_AFTER_MS = 60 * 60_000

// The fields of a change the reboot tile reads; a Change satisfies it.
export interface RebootChange {
  action: string
  state: string
  output: unknown
}

type Phase =
  | { kind: "idle" }
  | { kind: "scheduled"; at: number }
  | { kind: "rebooting"; at: number }
  | { kind: "not-back" }
  | { kind: "not-started" }
  | { kind: "back"; bootTime: number }

function rebootOutput(change: RebootChange | null | undefined): RebootOutput | null {
  if (change?.action !== REBOOT_ACTION || change.state !== "completed") return null
  const out = change.output as Partial<RebootOutput> | null
  if (typeof out?.scheduledAt !== "string" || typeof out.bootTimeBefore !== "string") return null
  return { scheduledAt: out.scheduledAt, bootTimeBefore: out.bootTimeBefore }
}

// rebootPhase derives the reboot state from the last reboot change's output,
// the host's connection and its boot time, never from what this page saw,
// so a reload shows the same state.
function rebootPhase(online: boolean, bootTime: string | undefined, out: RebootOutput | null, now: number): Phase {
  if (out === null) return { kind: "idle" }
  const at = Date.parse(out.scheduledAt)
  const before = Date.parse(out.bootTimeBefore)
  const boot = bootTime === undefined ? Number.NaN : Date.parse(bootTime)
  // A host seen with a newer boot time came back: going offline later is
  // not this reboot's doing.
  const cameBack = boot > before
  if (!online) {
    if (cameBack) return { kind: "idle" }
    return now - at >= NOT_BACK_AFTER_MS ? { kind: "not-back" } : { kind: "rebooting", at }
  }
  if (now - at >= SETTLED_AFTER_MS) return { kind: "idle" }
  if (cameBack) return { kind: "back", bootTime: boot }
  if (Number.isNaN(boot)) return now < at + NOT_STARTED_AFTER_MS ? { kind: "scheduled", at } : { kind: "idle" }
  return now >= at + NOT_STARTED_AFTER_MS ? { kind: "not-started" } : { kind: "scheduled", at }
}

function phaseStatus(phase: Phase, state: RebootState | undefined): { status: Status; label: string; live: boolean } | undefined {
  switch (phase.kind) {
    case "scheduled":
      return { status: "info", label: "Reboot scheduled", live: true }
    case "rebooting":
      return { status: "info", label: "Rebooting", live: true }
    case "not-back":
      return { status: "error", label: "Not back after 10 minutes", live: true }
    case "not-started":
      return { status: "error", label: "Reboot did not start", live: true }
    case "back":
      return { status: "ok", label: "Back online", live: true }
    case "idle":
      if (state === undefined) return undefined
      return state.required ? { status: "warn", label: "Reboot needed", live: false } : { status: "ok", label: "No reboot needed", live: false }
  }
}

// useClock is the time now, counted once a second while ticking.
function useClock(fixed: Date | undefined, ticking: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (fixed !== undefined || !ticking) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [fixed, ticking])
  return fixed?.getTime() ?? now
}

function RebootDetails({ state, tips }: { state: RebootState; tips: boolean }) {
  const restarts = `${state.packages.length} ${state.packages.length === 1 ? "package" : "packages"} · ${state.services.length} ${state.services.length === 1 ? "service" : "services"} to restart`
  return (
    <>
      {state.kernel !== null && (
        <span className="flex flex-wrap items-center gap-1 text-sm">
          Kernel <span className="font-mono tabular-nums break-all">{state.kernel.running}</span>
          <ArrowRight aria-label="to" className="size-3.5" />
          <span className="font-mono tabular-nums break-all">{state.kernel.newest}</span>
        </span>
      )}
      {(state.packages.length > 0 || state.services.length > 0) && (
        <span className="text-muted-foreground flex items-center gap-1 text-sm">
          {restarts}
          {tips && (
            <InfoTip label="Packages and services">
              <ul className="font-mono text-xs">
                {[...state.packages, ...state.services].map((name) => (
                  <li key={name}>{name}</li>
                ))}
              </ul>
            </InfoTip>
          )}
        </span>
      )}
    </>
  )
}

export interface RebootTileProps {
  host: Host
  bootTime?: string // hostinfo.summary's bootTime
  lastReboot?: RebootChange | null // the host's latest system.host.reboot change
  now?: Date // fixes the clock; ticks once a second otherwise
  state?: RebootState
  read?: { pending: boolean; error: unknown; retry: () => void }
}

// RebootTile shows whether the host needs a reboot, the progress of the last
// reboot (UX-4), and starts a reboot through the change dialog.
export function RebootTile({ host, bootTime, lastReboot, now: fixedNow, state, read }: RebootTileProps) {
  const [pending, setPending] = useState<PendingChange | null>(null)
  const out = rebootOutput(lastReboot)
  const now = useClock(fixedNow, out !== null)
  const phase = rebootPhase(host.online, bootTime, out, now)
  const busy = phase.kind === "scheduled" || phase.kind === "rebooting"
  const offline = !host.online

  let body
  if (phase.kind === "rebooting") body = <span className="text-sm tabular-nums">{`For ${formatDuration(Math.max(0, now - phase.at) / 1000)}`}</span>
  else if (phase.kind === "scheduled") body = <span className="text-sm tabular-nums">{`At ${formatTime(new Date(phase.at).toISOString())}`}</span>
  else if (phase.kind === "back") body = <span className="text-sm tabular-nums">{`Up ${formatDuration(Math.max(0, now - phase.bootTime) / 1000)}`}</span>
  else if (state !== undefined) body = <RebootDetails state={state} tips={!offline} />
  else if (read?.error) body = <TileError error={read.error} onRetry={read.retry} disabled={offline} />
  else if (read?.pending && !offline) body = <TileSkeleton />

  return (
    <Tile
      icon={Power}
      title="Reboot"
      greyed={offline}
      loading={state === undefined && read?.pending === true && !offline}
      status={phaseStatus(phase, state)}
      actions={
        <Button
          size="sm"
          variant="outline"
          disabled={offline || busy}
          onClick={() =>
            setPending({
              action: REBOOT_ACTION,
              title: `Reboot ${host.name}`,
              input: {},
              note: host.isLocal ? "This host runs the panel: the panel goes away until the host is back." : undefined,
            })
          }
        >
          Reboot
        </Button>
      }
    >
      {body}
      <SystemChange hostId={host.id} pending={pending} onClose={() => setPending(null)} />
    </Tile>
  )
}
