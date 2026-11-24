import { useState } from "react"
import { Link } from "react-router"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangle, BellRing, MoreHorizontal, OctagonAlert } from "lucide-react"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { Switch } from "darkraise-ui/components/switch"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { errorMessage, readAction } from "@/api/client"
import type { AlertList, AlertRule, AlertRuleList, AlertSeverity, FiredAlert } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { ALERTS_QUERY_KEY, ALERT_RULES_QUERY_KEY, SEVERITY_LABELS, alertTitle, conditionText, minutesText, targetText, valueText } from "@/lib/alerts"
import { formatTime } from "@/lib/format"
import { usePageTitle } from "@/lib/page-title"
import { STACKED_BODY, STACKED_CELL, STACKED_FRAME, STACKED_HEAD, STACKED_ROW, STACKED_TABLE } from "@/lib/table"
import { applied } from "@/lib/users"
import { isAdmin } from "@/session/can"
import { useSession } from "@/session/use-session"
import { RuleForm } from "./rule-dialog"

// Alerts change when the evaluator runs after each minute's poll, which the
// events stream does not announce, so the list refetches every minute.
const ALERTS_REFRESH_MS = 60_000

type Dialogue = { kind: "form"; rule: AlertRule | null } | { kind: "change"; title: string; name: string; input: Record<string, unknown> }

function listAlerts(): Promise<AlertList> {
  return readAction<AlertList>("metrics.alert.list", 1)
}

function listRules(): Promise<AlertRuleList> {
  return readAction<AlertRuleList>("metrics.alert.list-rules", 1)
}

function SeverityBadge({ level }: { level: AlertSeverity }) {
  const Icon = level === "critical" ? OctagonAlert : AlertTriangle
  return (
    <Badge variant={level === "critical" ? "destructive" : "warning"}>
      <Icon aria-hidden className="size-3.5" />
      {SEVERITY_LABELS[level]}
    </Badge>
  )
}

function Loading({ label }: { label: string }) {
  return (
    <div className="flex flex-col gap-2" aria-label={label}>
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-full" />
      <Skeleton className="h-8 w-2/3" />
    </div>
  )
}

function LoadError({ thing, error, onRetry }: { thing: string; error: unknown; onRetry: () => void }) {
  return (
    <Alert variant="destructive" role="alert">
      <AlertDescription className="flex flex-col items-start gap-2">
        <span>{`Couldn't load ${thing}: ${errorMessage(error)}`}</span>
        <Button size="sm" variant="outline" onClick={onRetry}>
          Retry
        </Button>
      </AlertDescription>
    </Alert>
  )
}

function AlertsTable({ label, alerts, when }: { label: string; alerts: FiredAlert[]; when: string }) {
  return (
    <div className={STACKED_FRAME}>
      <Table aria-label={label} className={STACKED_TABLE}>
        <TableHeader className={STACKED_HEAD}>
          <TableRow>
            <TableHead>Severity</TableHead>
            <TableHead>Host</TableHead>
            <TableHead>Alert</TableHead>
            <TableHead className="text-right">Value</TableHead>
            <TableHead>{when}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody className={STACKED_BODY}>
          {alerts.map((a) => (
            <TableRow className={STACKED_ROW} key={`${a.ruleId}/${a.hostId}/${a.subject}`}>
              <TableCell data-label="Severity" className={STACKED_CELL}>
                <SeverityBadge level={a.severity} />
              </TableCell>
              <TableCell data-label="Host" className={`font-medium wrap-anywhere ${STACKED_CELL}`}>
                <Link to={`/hosts/${encodeURIComponent(a.hostId)}`} className="underline-offset-4 hover:underline">
                  {a.hostName}
                </Link>
              </TableCell>
              <TableCell data-label="Alert" className={`max-w-96 whitespace-normal wrap-anywhere ${STACKED_CELL}`}>{alertTitle(a)}</TableCell>
              <TableCell data-label="Value" className={`text-right tabular-nums ${STACKED_CELL}`}>{valueText(a)}</TableCell>
              <TableCell data-label={when} className={`tabular-nums ${STACKED_CELL}`}>{formatTime(a.since)}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function AlertsSection() {
  const alerts = useQuery({ queryKey: ALERTS_QUERY_KEY, queryFn: listAlerts, refetchInterval: ALERTS_REFRESH_MS })
  if (alerts.isPending) return <Loading label="Loading the alerts" />
  if (alerts.isError) return <LoadError thing="alerts" error={alerts.error} onRetry={() => void alerts.refetch()} />
  const firing = alerts.data.alerts.filter((a) => a.state === "firing")
  const resolved = alerts.data.alerts.filter((a) => a.state === "resolved")
  return (
    <>
      <section aria-labelledby="alerts-firing" className="flex flex-col gap-3">
        <h2 id="alerts-firing" className="text-base font-semibold">
          Firing
        </h2>
        {firing.length === 0 ? <p className="text-sm text-muted-foreground">No alerts are firing.</p> : <AlertsTable label="Firing alerts" alerts={firing} when="Since" />}
      </section>
      {resolved.length > 0 && (
        <section aria-labelledby="alerts-resolved" className="flex flex-col gap-3">
          <h2 id="alerts-resolved" className="text-base font-semibold">
            Resolved in the last 24 hours
          </h2>
          <AlertsTable label="Resolved alerts" alerts={resolved} when="Resolved" />
        </section>
      )}
    </>
  )
}

function RuleMenu({ rule, onPick }: { rule: AlertRule; onPick: (d: Dialogue) => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={`Actions for ${rule.name}`}>
          <MoreHorizontal aria-hidden />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => onPick({ kind: "form", rule })}>Edit</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => onPick({ kind: "change", title: `Delete ${rule.name}`, name: "metrics.alert.delete", input: { id: rule.id } })}>
          Delete
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// ruleInput is a rule as metrics.alert.update takes it, with enabled set;
// offline rules leave out the comparator and threshold they ignore.
function ruleInput(rule: AlertRule, enabled: boolean): Record<string, unknown> {
  const input: Record<string, unknown> = { id: rule.id, name: rule.name, metric: rule.metric, durationMin: rule.durationMin, severity: rule.severity, target: rule.target, enabled }
  if (rule.metric !== "offline") {
    input.comparator = rule.comparator
    input.threshold = rule.threshold
  }
  return input
}

function RulesSection({ onPick }: { onPick: (d: Dialogue) => void }) {
  const rules = useQuery({ queryKey: ALERT_RULES_QUERY_KEY, queryFn: listRules })
  let body
  if (rules.isPending) body = <Loading label="Loading the alert rules" />
  else if (rules.isError) body = <LoadError thing="alert rules" error={rules.error} onRetry={() => void rules.refetch()} />
  else if (rules.data.rules.length === 0) body = <EmptyState icon={<BellRing aria-hidden />} title="No alert rules" description="Add one to hear about busy or full hosts." />
  else
    body = (
      <div className={STACKED_FRAME}>
        <Table aria-label="Alert rules" className={STACKED_TABLE}>
          <TableHeader className={STACKED_HEAD}>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Condition</TableHead>
              <TableHead>For</TableHead>
              <TableHead>Severity</TableHead>
              <TableHead>Hosts</TableHead>
              <TableHead>Enabled</TableHead>
              <TableHead className="w-10">
                <span className="sr-only">Actions</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody className={STACKED_BODY}>
            {rules.data.rules.map((r) => (
              <TableRow className={STACKED_ROW} key={r.id}>
                <TableCell data-label="Name" className={`max-w-72 font-medium whitespace-normal wrap-anywhere ${STACKED_CELL}`}>{r.name}</TableCell>
                <TableCell data-label="Condition" className={STACKED_CELL}>{conditionText(r)}</TableCell>
                <TableCell data-label="For" className={STACKED_CELL}>{minutesText(r.durationMin)}</TableCell>
                <TableCell data-label="Severity" className={STACKED_CELL}>
                  <SeverityBadge level={r.severity} />
                </TableCell>
                <TableCell data-label="Hosts" className={`max-w-72 whitespace-normal wrap-anywhere ${STACKED_CELL}`}>{targetText(r)}</TableCell>
                <TableCell data-label="Enabled" className={STACKED_CELL}>
                  <div className="flex items-center gap-2">
                    <Switch
                      checked={r.enabled}
                      aria-label={`${r.name} enabled`}
                      onCheckedChange={(on) =>
                        onPick({ kind: "change", title: on ? `Turn on ${r.name}` : `Turn off ${r.name}`, name: "metrics.alert.update", input: ruleInput(r, on) })
                      }
                    />
                    <span className="text-sm text-muted-foreground">{r.enabled ? "On" : "Off"}</span>
                  </div>
                </TableCell>
                <TableCell data-label="Actions" className={STACKED_CELL}>
                  <RuleMenu rule={r} onPick={onPick} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    )
  return (
    <section aria-labelledby="alerts-rules" className="flex flex-col gap-3">
      <h2 id="alerts-rules" className="text-base font-semibold">
        Rules
      </h2>
      {body}
    </section>
  )
}

// AlertsPage lists firing alerts first, then those resolved in the last day;
// an admin also gets the rules. Members cannot read the rules (fleet data),
// so they see only the alerts of the hosts they can see.
export function AlertsPage() {
  usePageTitle("Alerts")
  const client = useQueryClient()
  const { session } = useSession()
  const admin = isAdmin(session)
  const [dialogue, setDialogue] = useState<Dialogue | null>(null)
  const close = () => setDialogue(null)

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Alerts"
        description={admin ? "Firing alerts, and the rules that raise them." : "Firing alerts on the hosts you can see."}
        actions={admin ? <Button onClick={() => setDialogue({ kind: "form", rule: null })}>New rule</Button> : undefined}
      />
      <AlertsSection />
      {admin && <RulesSection onPick={setDialogue} />}
      {dialogue?.kind === "form" && (
        <RuleForm
          rule={dialogue.rule}
          onCancel={close}
          onPlan={(input) =>
            setDialogue(
              dialogue.rule === null
                ? { kind: "change", title: `Create the rule ${String(input.name)}`, name: "metrics.alert.create", input }
                : { kind: "change", title: `Edit ${dialogue.rule.name}`, name: "metrics.alert.update", input },
            )
          }
        />
      )}
      {dialogue?.kind === "change" && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : close())}
          title={dialogue.title}
          target={{ name: dialogue.name, version: 1 }}
          input={dialogue.input}
          onApplied={(change) => {
            if (!applied(change)) return
            void client.invalidateQueries({ queryKey: ALERT_RULES_QUERY_KEY })
            void client.invalidateQueries({ queryKey: ALERTS_QUERY_KEY })
          }}
        />
      )}
    </div>
  )
}
