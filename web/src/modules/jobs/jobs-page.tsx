import { useRef, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { MoreHorizontal, Plus } from "lucide-react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, HOSTS_QUERY_KEY, listHosts, readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { StatusPill } from "@/components/visual/status-pill"
import { formatTime } from "@/lib/format"
import { FALLBACK_FOCUS, useFocusFallback, useMenuDialogFocus } from "@/lib/return-focus"
import { applied } from "@/lib/users"
import { JournalPanel } from "@/modules/services/journal-panel"
import { useCanOnHost } from "@/session/can"
import { JobForm, type JobInput } from "./job-form"
import { jobUnit, lastResult, scheduleText, type Job, type JobList } from "./jobs"
import { focusableScroller } from "@/lib/table"

// Pending is one change a menu or the form asks for, planned in ChangeDialog.
interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
  done: string
}

type Pick = Pending | "edit" | "output"

function menuOf(job: Job): [string, Pick][] {
  const s = job.slug
  return [
    ["Run now", { action: "jobs.job.run", title: `Run ${s} now`, input: { slug: s }, done: "Started" }],
    ["Edit", "edit"],
    ["View output", "output"],
    job.enabled
      ? ["Disable", { action: "jobs.job.disable", title: `Disable ${s}`, input: { slug: s }, done: `${s} disabled` }]
      : ["Enable", { action: "jobs.job.enable", title: `Enable ${s}`, input: { slug: s }, done: `${s} enabled` }],
    ["Delete", { action: "jobs.job.delete", title: `Delete ${s}`, input: { slug: s }, done: `${s} deleted` }],
  ]
}

function JobMenu({ job, onPick }: { job: Job; onPick: (j: Job, p: Pick) => void }) {
  const { trigger, pick, onCloseAutoFocus } = useMenuDialogFocus((p: Pick) => onPick(job, p))
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button ref={trigger} variant="ghost" size="icon" aria-label={`Actions for ${job.slug}`}>
          <MoreHorizontal aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" onCloseAutoFocus={onCloseAutoFocus}>
        {menuOf(job).map(([label, item]) => (
          <DropdownMenuItem key={label} onSelect={() => pick(item)}>
            {label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function JobsTable({ jobs, admin, onPick }: { jobs: Job[]; admin: boolean; onPick: (j: Job, p: Pick) => void }) {
  return (
    <Table aria-label="Darkpanel jobs" ref={focusableScroller("Darkpanel jobs table")}>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Schedule</TableHead>
          <TableHead>User</TableHead>
          <TableHead>Last result</TableHead>
          <TableHead>Next run</TableHead>
          {admin && (
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          )}
        </TableRow>
      </TableHeader>
      <TableBody>
        {jobs.map((j) => {
          const [status, label] = lastResult(j)
          const words = scheduleText(j.schedule)
          return (
            <TableRow key={j.slug}>
              <TableCell className="max-w-[24rem] min-w-40 whitespace-normal wrap-anywhere">
                <span className="font-mono text-sm">{j.slug}</span>
                {(j.description ?? "") !== "" && <span className="block text-xs text-muted-foreground">{j.description}</span>}
                {(j.command ?? "") !== "" && <span className="block max-w-48 truncate font-mono text-xs text-muted-foreground md:max-w-96" title={j.command}>{j.command}</span>}
              </TableCell>
              <TableCell>
                {words !== "" && <span className="block">{words}</span>}
                <span className="block font-mono text-xs text-muted-foreground">{j.schedule}</span>
              </TableCell>
              <TableCell>{j.user}</TableCell>
              <TableCell>
                <StatusPill status={status} label={label} />
              </TableCell>
              <TableCell className="whitespace-nowrap">{!j.enabled ? "Disabled" : (j.nextRun ?? "") === "" ? "—" : formatTime(j.nextRun ?? null)}</TableCell>
              {admin && (
                <TableCell>
                  <JobMenu job={j} onPick={onPick} />
                </TableCell>
              )}
            </TableRow>
          )
        })}
      </TableBody>
    </Table>
  )
}

function OtherTasks({ list }: { list: JobList }) {
  if (list.timers.length === 0 && list.cron.length === 0) return null
  return (
    <section aria-labelledby="jobs-other" className="flex flex-col gap-2">
      <h3 id="jobs-other" className="text-sm font-semibold">
        Other scheduled tasks
      </h3>
      <p className="text-xs text-muted-foreground">Timers and cron entries outside Darkpanel; change them in their files.</p>
      <Table aria-label="Other scheduled tasks" ref={focusableScroller("Other scheduled tasks table")}>
        <TableHeader>
          <TableRow>
            <TableHead>Task</TableHead>
            <TableHead>Schedule</TableHead>
            <TableHead>User</TableHead>
            <TableHead>Source</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {list.timers.map((t) => (
            <TableRow key={t.name}>
              <TableCell className="min-w-40 whitespace-normal wrap-anywhere font-mono text-xs">{t.name}</TableCell>
              <TableCell className="whitespace-nowrap">{(t.nextRun ?? "") === "" ? "Not scheduled" : `Next ${formatTime(t.nextRun ?? null)}`}</TableCell>
              <TableCell>root</TableCell>
              <TableCell className="min-w-40 whitespace-normal wrap-anywhere font-mono text-xs">{t.source}</TableCell>
            </TableRow>
          ))}
          {list.cron.map((c, i) => (
            <TableRow key={`${c.source}-${i}`}>
              <TableCell className="max-w-[24rem] min-w-40 whitespace-normal wrap-anywhere font-mono text-xs">{c.command ?? "Cron entry"}</TableCell>
              <TableCell className="font-mono text-xs">{c.schedule}</TableCell>
              <TableCell>{c.user}</TableCell>
              <TableCell className="min-w-40 whitespace-normal wrap-anywhere font-mono text-xs">{c.source}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </section>
  )
}

function Loading() {
  return (
    <div className="flex flex-col gap-2" role="status" aria-label="Loading the jobs">
      {[0, 1, 2].map((i) => (
        <Skeleton key={i} className="h-10 w-full" />
      ))}
    </div>
  )
}

// JobsPage is the Jobs tab (UI-7, UX-10): Darkpanel's jobs, which admins add,
// change and run, and the host's other timers and cron entries read-only.
export function JobsPage({ hostId }: { hostId: string }) {
  const admin = useCanOnHost("admin")
  const client = useQueryClient()
  const key = ["resource", hostId, "jobs.job.list"]
  const list = useQuery({ queryKey: key, queryFn: () => readAction<JobList>("jobs.job.list", 1, {}, hostId) })
  const hosts = useQuery({ queryKey: HOSTS_QUERY_KEY, queryFn: listHosts })
  const hostName = hosts.data?.hosts.find((h) => h.id === hostId)?.name ?? hostId
  const [form, setForm] = useState<{ job?: Job } | null>(null)
  const [pending, setPending] = useState<Pending | null>(null)
  const [output, setOutput] = useState<string | null>(null)
  const outputRef = useRef<HTMLElement>(null)
  useFocusFallback(form !== null || pending !== null)

  function onPick(job: Job, p: Pick) {
    if (p === "edit") setForm({ job })
    else if (p === "output") {
      setOutput(jobUnit(job.slug))
      window.setTimeout(() => outputRef.current?.focus(), 0)
    } else setPending(p)
  }

  // Whether the reviewed change applied: only then does the form close.
  const done = useRef(false)

  function review(input: JobInput, job?: Job) {
    done.current = false
    setPending(
      job === undefined
        ? { action: "jobs.job.create", title: `Add ${input.slug}`, input: { ...input }, done: `${input.slug} added` }
        : { action: "jobs.job.update", title: `Change ${input.slug}`, input: { ...input }, done: `${input.slug} changed` },
    )
  }

  const addButton = (
    <Button {...{ [FALLBACK_FOCUS]: "" }} onClick={() => setForm({})}>
      <Plus aria-hidden="true" />
      Add job
    </Button>
  )

  return (
    <div className="flex flex-col gap-4">
      <h2 className="sr-only">Jobs</h2>
      {list.isPending ? (
        <Loading />
      ) : list.isError ? (
        <Alert variant="destructive" role="alert">
          <AlertTitle as="h3">Couldn't load the jobs</AlertTitle>
          <AlertDescription className="flex flex-col items-start gap-2">
            {errorMessage(list.error)}
            <Button size="sm" variant="outline" onClick={() => void list.refetch()}>
              Try again
            </Button>
          </AlertDescription>
        </Alert>
      ) : (
        <>
          {list.data.jobs.length === 0 ? (
            <EmptyState title="No Darkpanel jobs yet" description="A job runs a command on a schedule as the user you pick." action={admin ? addButton : undefined} />
          ) : (
            <>
              {admin && <div className="flex justify-end">{addButton}</div>}
              <JobsTable jobs={list.data.jobs} admin={admin} onPick={onPick} />
            </>
          )}
          <OtherTasks list={list.data} />
        </>
      )}
      {output !== null && (
        <section ref={outputRef} tabIndex={-1} aria-label={`Output of ${output}`} className="flex flex-col gap-2 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-sm font-semibold">Output</h3>
            <Button size="sm" variant="outline" onClick={() => setOutput(null)}>
              Close output
            </Button>
          </div>
          <JournalPanel hostId={hostId} unit={output} onUnitChange={setOutput} />
        </section>
      )}
      {form !== null && (
        <JobForm
          hostId={hostId}
          hostName={hostName}
          job={form.job}
          hidden={pending !== null}
          onCancel={() => setForm(null)}
          onReview={(input) => review(input, form.job)}
        />
      )}
      {pending !== null && (
        <ChangeDialog
          open
          onOpenChange={(open) => {
            if (open) return
            setPending(null)
            if (done.current) setForm(null)
          }}
          title={pending.title}
          target={{ name: pending.action, version: 1, hostId, module: "jobs" }}
          input={pending.input}
          onApplied={(c: Change) => {
            void client.invalidateQueries({ queryKey: key })
            if (applied(c)) {
              done.current = true
              toast(pending.done)
            }
          }}
        />
      )}
    </div>
  )
}
