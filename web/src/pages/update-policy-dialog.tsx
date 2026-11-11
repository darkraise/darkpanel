import { useEffect, useRef, useState, type FormEvent } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { HOSTS_QUERY_KEY, changeError, changeInProgress } from "@/api/client"
import type { Channel, Host } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"

const DAYS: [id: string, label: string][] = [
  ["mon", "Monday"],
  ["tue", "Tuesday"],
  ["wed", "Wednesday"],
  ["thu", "Thursday"],
  ["fri", "Friday"],
  ["sat", "Saturday"],
  ["sun", "Sunday"],
]

const TIME = /^([01][0-9]|2[0-3]):[0-5][0-9]$/

type Phase = "closed" | "form" | "plan"

export function UpdatePolicyButton({ host }: { host: Host }) {
  const client = useQueryClient()
  const [phase, setPhase] = useState<Phase>("closed")
  const [channel, setChannel] = useState<Channel>("stable")
  const [scheduled, setScheduled] = useState(false)
  const [days, setDays] = useState<string[]>([])
  const [time, setTime] = useState("")
  const [errors, setErrors] = useState<{ days?: string; time?: string }>({})
  const [input, setInput] = useState<Record<string, unknown>>({})
  const firstDay = useRef<HTMLButtonElement>(null)
  const timeField = useRef<HTMLInputElement>(null)
  const [stable, setStable] = useState<HTMLButtonElement | null>(null)

  // darkraise-ui's focus trap focuses the first tabbable control when the
  // dialog mounts, before the radio group makes its checked radio tabbable,
  // so it lands on the schedule checkbox; the channel is focused a frame later.
  // The dialog's portal mounts its content a render after it opens, so the
  // frame is scheduled once the radio exists, not when the phase changes.
  useEffect(() => {
    if (phase !== "form" || stable === null) return
    const frame = requestAnimationFrame(() => stable.focus())
    return () => cancelAnimationFrame(frame)
  }, [phase, stable])

  function reset() {
    setPhase("closed")
    setChannel("stable")
    setScheduled(false)
    setDays([])
    setTime("")
    setErrors({})
    setInput({})
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const next: { days?: string; time?: string } = {}
    if (scheduled && days.length === 0) next.days = "Choose at least one day."
    if (scheduled && !TIME.test(time)) next.time = "Enter a UTC time such as 03:00."
    setErrors(next)
    if (next.days !== undefined) return firstDay.current?.focus()
    if (next.time !== undefined) return timeField.current?.focus()
    const ordered = DAYS.map(([id]) => id).filter((id) => days.includes(id))
    setInput({ hostId: host.id, channel, schedule: scheduled ? { days: ordered, time } : null })
    setPhase("plan")
  }

  return (
    <>
      <Button variant="outline" onClick={() => setPhase("form")}>
        Change update policy
      </Button>
      <Dialog open={phase === "form"} onOpenChange={(next) => (next ? undefined : reset())}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{`Update policy for ${host.name}`}</DialogTitle>
            <DialogDescription>The plan shows the stored policy beside the new one before anything changes.</DialogDescription>
          </DialogHeader>
          <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
            <fieldset className="flex flex-col gap-2">
              <legend className="text-sm font-medium">Channel</legend>
              <RadioGroup value={channel} onValueChange={(v) => setChannel(v as Channel)} aria-label="Channel" className="flex flex-wrap gap-4">
                <div className="flex items-center gap-2">
                  <RadioGroupItem ref={setStable} id="policy-channel-stable" value="stable" />
                  <Label htmlFor="policy-channel-stable">Stable</Label>
                </div>
                <div className="flex items-center gap-2">
                  <RadioGroupItem id="policy-channel-beta" value="beta" />
                  <Label htmlFor="policy-channel-beta">Beta</Label>
                </div>
              </RadioGroup>
            </fieldset>
            <div className="flex items-center gap-2">
              <Checkbox id="policy-scheduled" checked={scheduled} onCheckedChange={(c) => setScheduled(c === true)} />
              <Label htmlFor="policy-scheduled">Update on a schedule</Label>
            </div>
            {scheduled && (
              <>
                <fieldset className="flex flex-col gap-2" aria-describedby={errors.days !== undefined ? "policy-days-error" : undefined}>
                  <legend className="text-sm font-medium">Days</legend>
                  <div className="flex flex-wrap gap-4">
                    {DAYS.map(([id, label], i) => (
                      <div key={id} className="flex items-center gap-2">
                        <Checkbox
                          ref={i === 0 ? firstDay : undefined}
                          id={`policy-day-${id}`}
                          checked={days.includes(id)}
                          aria-invalid={errors.days !== undefined}
                          onCheckedChange={(c) => setDays((cur) => (c === true ? [...cur, id] : cur.filter((d) => d !== id)))}
                        />
                        <Label htmlFor={`policy-day-${id}`}>{label}</Label>
                      </div>
                    ))}
                  </div>
                  {errors.days !== undefined && (
                    <p id="policy-days-error" className="text-sm text-destructive">
                      {errors.days}
                    </p>
                  )}
                </fieldset>
                <div className="flex flex-col gap-2">
                  <Label htmlFor="policy-time">Time (UTC)</Label>
                  <Input
                    ref={timeField}
                    id="policy-time"
                    type="text"
                    inputMode="numeric"
                    placeholder="03:00"
                    autoComplete="off"
                    value={time}
                    onChange={(e) => setTime(e.target.value)}
                    aria-invalid={errors.time !== undefined}
                    aria-describedby={errors.time !== undefined ? "policy-time-error" : undefined}
                  />
                  {errors.time !== undefined && (
                    <p id="policy-time-error" className="text-sm text-destructive">
                      {errors.time}
                    </p>
                  )}
                </div>
              </>
            )}
            <DialogFooter>
              <Button type="submit">Continue</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <ChangeDialog
        open={phase === "plan"}
        onOpenChange={(next) => (next ? undefined : reset())}
        title={`Update policy for ${host.name}`}
        target={{ name: "core.host.update-policy", version: 1 }}
        input={input}
        onApplied={(change) => {
          if (changeError(change) === null && !changeInProgress(change)) void client.invalidateQueries({ queryKey: HOSTS_QUERY_KEY })
        }}
      />
    </>
  )
}
