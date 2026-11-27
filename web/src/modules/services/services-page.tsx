import { useRef, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { MoreHorizontal, Search } from "lucide-react"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { toast } from "darkraise-ui/components/sonner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { readAction } from "@/api/client"
import type { Change } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { StatusPill } from "@/components/visual/status-pill"
import type { Status } from "@/components/visual/status"
import { applied } from "@/lib/users"
import { useFocusFallback, useMenuDialogFocus } from "@/lib/return-focus"
import { useCanOnHost } from "@/session/can"
import { JournalPanel } from "./journal-panel"
import { focusableScroller } from "@/lib/table"

// Unit is one row of services.unit.list.
export interface Unit {
  name: string
  type: string
  description: string
  load: string
  active: string
  sub: string
  enabled: string
  mainPid?: number
}

export const OWN_UNITS_REASON = "Darkpanel manages its own units"

type StateId = "running" | "exited" | "failed" | "inactive" | "other"
const STATES: [StateId | "all", string][] = [
  ["all", "All states"],
  ["running", "Running"],
  ["exited", "Exited"],
  ["failed", "Failed"],
  ["inactive", "Inactive"],
]

function stateOf(u: Unit): [StateId, Status, string] {
  if (u.active === "failed") return ["failed", "error", "Failed"]
  if (u.sub === "running") return ["running", "ok", "Running"]
  if (u.sub === "exited") return ["exited", "info", "Exited"]
  if (u.active === "inactive") return ["inactive", "off", "Inactive"]
  return ["other", "info", u.sub === "" ? u.active : u.sub.charAt(0).toUpperCase() + u.sub.slice(1)]
}

function atBoot(enabled: string): string {
  if (enabled === "") return "No unit file"
  return enabled.charAt(0).toUpperCase() + enabled.slice(1)
}

type Op = "start" | "stop" | "restart" | "reload" | "enable" | "disable"
const OP_LABEL: Record<Op, string> = { start: "Start", stop: "Stop", restart: "Restart", reload: "Reload", enable: "Enable", disable: "Disable" }
const DONE: Record<Op, string> = { start: "started", stop: "stopped", restart: "restarted", reload: "reloaded", enable: "enabled at boot", disable: "disabled at boot" }

// UnitMenu is a row's menu: operators drive the unit, admins also enable and
// disable it at boot; Darkpanel's own units show the items disabled.
function UnitMenu({ unit, admin, onOp, onLogs }: { unit: Unit; admin: boolean; onOp: (op: Op) => void; onLogs: () => void }) {
  const own = unit.name.startsWith("darkpanel-")
  const ops: Op[] = admin ? ["start", "stop", "restart", "reload", "enable", "disable"] : ["start", "stop", "restart", "reload"]
  const { trigger, pick, onCloseAutoFocus } = useMenuDialogFocus(onOp)
  // logs: View logs was picked, so the closing menu leaves focus to the
  // journal's unit field instead of taking it back to its button.
  const logs = useRef(false)
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button ref={trigger} variant="ghost" size="icon" aria-label={`Actions for ${unit.name}`}>
          <MoreHorizontal aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="end"
        onCloseAutoFocus={(e) => {
          if (!logs.current) return onCloseAutoFocus(e)
          logs.current = false
          e.preventDefault()
          document.getElementById("journal-unit")?.focus()
        }}
      >
        {ops.map((op) => (
          <DropdownMenuItem key={op} disabled={own} onSelect={() => pick(op)}>
            <span className="flex flex-col">
              {OP_LABEL[op]}
              {own && <span className="text-xs text-muted-foreground">{OWN_UNITS_REASON}</span>}
            </span>
          </DropdownMenuItem>
        ))}
        <DropdownMenuItem
          onSelect={() => {
            logs.current = true
            onLogs()
          }}
        >
          View logs
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function Loading() {
  return (
    <div className="flex flex-col gap-2" role="status" aria-label="Loading the services">
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-2/3" />
    </div>
  )
}

// ServicesPage lists the host's units with their state and drives them
// (UI-1, UX-2); the journal panel below shows their logs (UI-2).
export function ServicesPage({ hostId }: { hostId: string }) {
  const operate = useCanOnHost("operate")
  const admin = useCanOnHost("admin")
  const client = useQueryClient()
  const key = ["resource", hostId, "services.unit.list"]
  const units = useQuery({ queryKey: key, queryFn: () => readAction<{ units: Unit[] }>("services.unit.list", 1, {}, hostId) })
  const [search, setSearch] = useState("")
  const [state, setState] = useState<StateId | "all">("all")
  const [change, setChange] = useState<{ unit: Unit; op: Op } | null>(null)
  const [logsUnit, setLogsUnit] = useState("")

  useFocusFallback(change !== null)

  let body
  if (units.isPending) body = <Loading />
  else if (units.isError)
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>Couldn't load services</span>
          <Button size="sm" variant="outline" onClick={() => void units.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  else {
    const needle = search.trim().toLowerCase()
    const shown = units.data.units.filter(
      (u) => (state === "all" || stateOf(u)[0] === state) && (needle === "" || u.name.toLowerCase().includes(needle) || u.description.toLowerCase().includes(needle)),
    )
    body =
      shown.length === 0 ? (
        <EmptyState
          title={needle === "" ? "No units in this state" : `No units match '${search.trim()}'`}
          action={
            <Button
              variant="outline"
              onClick={() => {
                setSearch("")
                setState("all")
              }}
            >
              Clear filter
            </Button>
          }
        />
      ) : (
        <Table ref={focusableScroller("Units table")}>
          <TableHeader>
            <TableRow>
              <TableHead>Unit</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>State</TableHead>
              <TableHead>At boot</TableHead>
              {operate && (
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              )}
            </TableRow>
          </TableHeader>
          <TableBody>
            {shown.map((u) => {
              const [, status, label] = stateOf(u)
              return (
                <TableRow key={u.name}>
                  <TableCell className="max-w-[16rem] font-mono text-xs sm:max-w-[24rem]">
                    <span className="block truncate" title={u.name}>
                      {u.name}
                    </span>
                  </TableCell>
                  <TableCell className="max-w-[20rem]">
                    <span className="block truncate" title={u.description}>
                      {u.description}
                    </span>
                  </TableCell>
                  <TableCell>
                    <StatusPill status={status} label={label} />
                  </TableCell>
                  <TableCell>{atBoot(u.enabled)}</TableCell>
                  {operate && (
                    <TableCell>
                      <UnitMenu unit={u} admin={admin} onOp={(op) => setChange({ unit: u, op })} onLogs={() => setLogsUnit(u.name)} />
                    </TableCell>
                  )}
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      )
  }

  return (
    <div className="flex flex-col gap-6">
      <h2 className="sr-only">Services</h2>
      <div className="flex flex-wrap items-end gap-3">
        <div className="flex flex-col gap-1">
          <Label htmlFor="services-search">Search units</Label>
          <Input id="services-search" value={search} placeholder="Name or description" prefix={<Search aria-hidden="true" />} onChange={(e) => setSearch(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1">
          <Label htmlFor="services-state">State</Label>
          <select
            id="services-state"
            value={state}
            onChange={(e) => setState(e.target.value as StateId | "all")}
            className="h-9 rounded-md border border-input bg-background px-2 text-sm"
          >
            {STATES.map(([id, text]) => (
              <option key={id} value={id}>
                {text}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="overflow-x-auto">{body}</div>
      <JournalPanel hostId={hostId} unit={logsUnit} onUnitChange={setLogsUnit} />
      {change !== null && (
        <ChangeDialog
          open
          onOpenChange={(open) => (open ? undefined : setChange(null))}
          title={`${OP_LABEL[change.op]} ${change.unit.name}`}
          target={{ name: `services.unit.${change.op}`, version: 1, hostId, module: "services" }}
          input={{ unit: change.unit.name }}
          onApplied={(c: Change) => {
            if (!applied(c)) return
            toast(`${change.unit.name} ${DONE[change.op]}`)
            void client.invalidateQueries({ queryKey: key })
          }}
        />
      )}
    </div>
  )
}
