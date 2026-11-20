import { useEffect, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Clock } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { changeQueryKey, confirmChange, errorMessage, listSteps, revertChange, stepsQueryKey } from "@/api/client"
import { Stepper } from "@/components/visual/stepper"
import { latestInstallKey, useLatestInstall } from "./install-lookup"
import { installSteps, lastDeadline } from "./install-steps"

function useClock(running: boolean): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!running) return
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [running])
  return now
}

function mmss(seconds: number): string {
  const s = Math.max(0, Math.round(seconds))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`
}

// Confirmation is the firewall step's window: the time left, Keep and
// Revert (UX-5).
export function Confirmation({ changeId, deadline, onDecided }: { changeId: string; deadline: string | null; onDecided: (kept: boolean) => void }) {
  const client = useQueryClient()
  const now = useClock(deadline !== null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  async function decide(keep: boolean) {
    setBusy(true)
    setError(null)
    try {
      const next = await (keep ? confirmChange(changeId) : revertChange(changeId))
      client.setQueryData(changeQueryKey(changeId), next)
      void client.invalidateQueries({ queryKey: stepsQueryKey(changeId) })
      onDecided(keep)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-3">
        {deadline !== null && (
          <span role="timer" aria-live="off" className="flex items-center gap-1 text-sm">
            <Clock aria-hidden className="size-4" />
            Reverts in <span className="font-mono tabular-nums">{mmss((Date.parse(deadline) - now) / 1000)}</span>
          </span>
        )}
        <Button loading={busy} onClick={() => void decide(true)}>
          Keep
        </Button>
        <Button variant="outline" disabled={busy} onClick={() => void decide(false)}>
          Revert
        </Button>
      </div>
      {error !== null && (
        <Alert variant="destructive" role="alert">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
    </div>
  )
}

// PendingDockerInstall keeps an install's confirmation window on the Docker
// page: once the packages are in, the module turns available and the page
// shows Docker instead of the install panel while the firewall step still
// awaits Keep or Revert.
export function PendingDockerInstall({ hostId }: { hostId: string }) {
  const client = useQueryClient()
  const latest = useLatestInstall(hostId, true)
  const awaiting = latest.data?.state === "awaiting_confirmation" ? latest.data : undefined
  const steps = useQuery({
    queryKey: stepsQueryKey(awaiting?.id ?? ""),
    queryFn: () => listSteps(awaiting?.id ?? ""),
    enabled: awaiting !== undefined,
  })
  if (awaiting === undefined) return null
  return (
    <Alert variant="warning" role="region" aria-label="Docker install awaiting confirmation" data-testid="state-pendingInstall" className="mb-4">
      <AlertTitle as="h2">Keep the Docker install?</AlertTitle>
      <AlertDescription className="flex flex-col gap-3">
        <Stepper steps={installSteps(true)} current="confirm" />
        <Confirmation
          changeId={awaiting.id}
          deadline={lastDeadline(steps.data)}
          onDecided={() => void client.invalidateQueries({ queryKey: latestInstallKey(hostId) })}
        />
      </AlertDescription>
    </Alert>
  )
}
