import { useEffect, useState, type FormEvent } from "react"
import { useQuery } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "darkraise-ui/components/select"
import { Textarea } from "darkraise-ui/components/textarea"
import { errorMessage, readAction } from "@/api/client"
import { formatTime } from "@/lib/format"
import type { Account } from "@/modules/users/keys"
import { SLUG_PATTERN, type Job } from "./jobs"

// JobInput is the input of jobs.job.create and jobs.job.update.
export interface JobInput {
  slug: string
  description: string
  schedule: string
  user: string
  command: string
  timeoutSeconds: number
  catchUp: boolean
  randomDelaySeconds: number
}

function FieldError({ id, children }: { id: string; children: string }) {
  return (
    <p id={id} className="text-sm text-destructive-text">
      {children}
    </p>
  )
}

function whole(text: string, min: number, max: number): number | null {
  if (!/^\d+$/.test(text)) return null
  const n = Number(text)
  return n >= min && n <= max ? n : null
}

// useSettled is value once it has held still for ms.
function useSettled(value: string, ms: number): string {
  const [settled, setSettled] = useState(value)
  useEffect(() => {
    const id = window.setTimeout(() => setSettled(value), ms)
    return () => window.clearTimeout(id)
  }, [value, ms])
  return settled
}

export interface JobFormProps {
  hostId: string
  hostName: string
  // job is the job being edited; none adds one.
  job?: Job
  onCancel: () => void
  onReview: (input: JobInput) => void
}

// JobForm asks a job's fields (UI-7, UX-10); the schedule shows the agent's
// next three runs, or its error, as it is typed.
export function JobForm({ hostId, hostName, job, onCancel, onReview }: JobFormProps) {
  const [slug, setSlug] = useState(job?.slug ?? "")
  const [description, setDescription] = useState(job?.description ?? "")
  const [schedule, setSchedule] = useState(job?.schedule ?? "")
  const [user, setUser] = useState(job?.user ?? "root")
  const [command, setCommand] = useState(job?.command ?? "")
  const [timeoutText, setTimeoutText] = useState(String(job?.timeoutSeconds ?? 3600))
  const [delay, setDelay] = useState(String(job?.randomDelaySeconds ?? 0))
  const [catchUp, setCatchUp] = useState(job?.catchUp ?? false)
  const [submitted, setSubmitted] = useState(false)

  const settled = useSettled(schedule.trim(), 400)
  const check = useQuery({
    queryKey: ["resource", hostId, "jobs.schedule.check", settled],
    queryFn: () => readAction<{ next: string[] }>("jobs.schedule.check", 1, { schedule: settled }, hostId),
    enabled: settled !== "",
    retry: false,
  })
  const accounts = useQuery({
    queryKey: ["resource", hostId, "users.user.list"],
    queryFn: () => readAction<{ accounts: Account[] }>("users.user.list", 1, {}, hostId),
    retry: false,
  })
  const users = ["root", ...(accounts.data?.accounts ?? []).map((a) => a.name).filter((n) => n !== "root")]
  if (!users.includes(user)) users.push(user)

  const errors = {
    slug: job !== undefined ? null : slug === "" ? "Enter a name" : SLUG_PATTERN.test(slug) ? null : "Use lower case letters, digits and hyphens, at most 40",
    schedule: schedule.trim() === "" ? "Enter a schedule" : check.isError && settled === schedule.trim() ? errorMessage(check.error) : null,
    command: command.trim() === "" ? "Enter the command to run" : new TextEncoder().encode(command).length > 16384 ? "A command is at most 16 KiB" : null,
    timeout: whole(timeoutText, 60, 86400) === null ? "Use 60 to 86400 seconds" : null,
    delay: whole(delay, 0, 3600) === null ? "Use 0 to 3600 seconds" : null,
  }
  const show = (field: keyof typeof errors, value: string, initial: string) => (submitted || value !== initial ? errors[field] : null)
  const scheduleError = errors.schedule !== null && (submitted || check.isError) ? errors.schedule : null
  const pendingCheck = schedule.trim() !== "" && (settled !== schedule.trim() || check.isFetching)

  function submit(e: FormEvent) {
    e.preventDefault()
    setSubmitted(true)
    if (Object.values(errors).some((v) => v !== null) || pendingCheck || check.data === undefined) return
    onReview({
      slug,
      description: description.trim(),
      schedule: schedule.trim(),
      user,
      command,
      timeoutSeconds: Number(timeoutText),
      catchUp,
      randomDelaySeconds: Number(delay),
    })
  }

  const described = (id: string, error: string | null, extra?: string) => [error !== null ? `${id}-error` : "", extra ?? ""].filter((s) => s !== "").join(" ") || undefined
  const slugError = show("slug", slug, job?.slug ?? "")
  const commandError = show("command", command, job?.command ?? "")
  const timeoutError = show("timeout", timeoutText, String(job?.timeoutSeconds ?? 3600))
  const delayError = show("delay", delay, String(job?.randomDelaySeconds ?? 0))

  return (
    <Dialog open onOpenChange={(open) => !open && onCancel()}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
          <DialogHeader>
            <DialogTitle className="break-all">{job === undefined ? `Add a job on ${hostName}` : `Edit ${job.slug}`}</DialogTitle>
            <DialogDescription>Runs a command on a schedule as the user you pick.</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-2">
            <Label htmlFor="job-slug">Name</Label>
            <Input
              id="job-slug"
              value={slug}
              disabled={job !== undefined}
              onChange={(e) => setSlug(e.target.value)}
              className="font-mono"
              spellCheck={false}
              aria-invalid={slugError !== null}
              aria-describedby={described("job-slug", slugError)}
            />
            {slugError !== null && <FieldError id="job-slug-error">{slugError}</FieldError>}
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="job-description">Description</Label>
            <Input id="job-description" value={description} maxLength={200} onChange={(e) => setDescription(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="job-schedule">Schedule</Label>
            <Input
              id="job-schedule"
              value={schedule}
              onChange={(e) => setSchedule(e.target.value)}
              placeholder="*-*-* 03:00:00"
              className="font-mono"
              spellCheck={false}
              aria-invalid={scheduleError !== null}
              aria-describedby={described("job-schedule", scheduleError, "job-schedule-next")}
            />
            {scheduleError !== null && <FieldError id="job-schedule-error">{scheduleError}</FieldError>}
            <div id="job-schedule-next" aria-live="polite" className="text-xs text-muted-foreground">
              {check.data !== undefined && settled === schedule.trim() && !check.isError ? (
                <>
                  <span>Next runs</span>
                  <ul aria-label="Next runs" className="list-disc ps-4">
                    {check.data.next.map((t) => (
                      <li key={t}>{formatTime(t)}</li>
                    ))}
                  </ul>
                </>
              ) : scheduleError === null ? (
                <span>A valid schedule shows its next three runs here.</span>
              ) : null}
            </div>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="job-user">User</Label>
            <Select value={user} onValueChange={setUser}>
              <SelectTrigger id="job-user" className="w-full min-w-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {users.map((u) => (
                  <SelectItem key={u} value={u}>
                    {u}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="job-command">Command</Label>
            <Textarea
              id="job-command"
              value={command}
              onChange={(e) => setCommand(e.target.value)}
              rows={3}
              className="font-mono text-xs"
              spellCheck={false}
              aria-invalid={commandError !== null}
              aria-describedby={described("job-command", commandError)}
            />
            {commandError !== null && <FieldError id="job-command-error">{commandError}</FieldError>}
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="flex flex-col gap-1">
              <Label htmlFor="job-timeout">Timeout (seconds)</Label>
              <Input
                id="job-timeout"
                inputMode="numeric"
                value={timeoutText}
                onChange={(e) => setTimeoutText(e.target.value)}
                aria-invalid={timeoutError !== null}
                aria-describedby={described("job-timeout", timeoutError)}
              />
              {timeoutError !== null && <FieldError id="job-timeout-error">{timeoutError}</FieldError>}
            </div>
            <div className="flex flex-col gap-1">
              <Label htmlFor="job-delay">Random delay (seconds)</Label>
              <Input
                id="job-delay"
                inputMode="numeric"
                value={delay}
                onChange={(e) => setDelay(e.target.value)}
                aria-invalid={delayError !== null}
                aria-describedby={described("job-delay", delayError)}
              />
              {delayError !== null && <FieldError id="job-delay-error">{delayError}</FieldError>}
            </div>
          </div>
          <div className="flex items-center gap-2">
            <Checkbox id="job-catch-up" checked={catchUp} onCheckedChange={(v) => setCatchUp(v === true)} />
            <Label htmlFor="job-catch-up">Catch up a run missed while the host was off</Label>
          </div>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit">Review change</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
