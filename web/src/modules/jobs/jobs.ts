import type { Status } from "@/components/visual/status"

// Job is one Darkpanel job as jobs.job.list answers it; command is only in an
// admin's answer. Times are RFC 3339, "" when there is none.
export interface Job {
  slug: string
  description?: string
  schedule: string
  user: string
  command?: string
  timeoutSeconds: number
  catchUp: boolean
  randomDelaySeconds: number
  enabled: boolean
  running: boolean
  nextRun?: string
  lastStart?: string
  lastResult?: string
  exitStatus?: number | null
}

export interface OtherTimer {
  name: string
  unit: string
  source: string
  nextRun?: string
  lastRun?: string
}

export interface CronEntry {
  source: string
  schedule: string
  user: string
  command?: string
}

export interface JobList {
  jobs: Job[]
  timers: OtherTimer[]
  cron: CronEntry[]
}

// jobUnit is the service a job's runs log under.
export function jobUnit(slug: string): string {
  return `darkpanel-job-${slug}.service`
}

export const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{0,39}$/

const DAYS: Record<string, string> = { Mon: "Mondays", Tue: "Tuesdays", Wed: "Wednesdays", Thu: "Thursdays", Fri: "Fridays", Sat: "Saturdays", Sun: "Sundays" }

// scheduleText says a calendar expression in words for the shapes most jobs
// use, "" for any other, which is shown as the expression alone.
export function scheduleText(expr: string): string {
  const named: Record<string, string> = { minutely: "Every minute", hourly: "Every hour", daily: "Every day at 00:00", weekly: "Mondays at 00:00", monthly: "On the 1st at 00:00" }
  const e = expr.trim()
  if (named[e] !== undefined) return named[e]
  const m = /^(?:(Mon|Tue|Wed|Thu|Fri|Sat|Sun) )?\*-\*-\* (\d{2}):(\d{2})(?::00)?$/.exec(e)
  if (m === null) return ""
  const at = `${m[2]}:${m[3]}`
  return m[1] === undefined ? `Every day at ${at}` : `${DAYS[m[1]] ?? m[1]} at ${at}`
}

// lastResult is the status and text of a job's last run.
export function lastResult(job: Job): [Status, string] {
  if (job.running) return ["info", "Running"]
  const exit = job.exitStatus ?? null
  switch (job.lastResult ?? "") {
    case "success":
      return ["ok", "Succeeded"]
    case "exit-code":
      return ["error", exit === null ? "Failed" : `Failed (exit ${exit})`]
    case "timeout":
      return ["error", "Timed out"]
    case "":
      return ["off", "Not run yet"]
    default:
      return ["error", `Failed: ${job.lastResult ?? ""}`]
  }
}
