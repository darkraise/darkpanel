import { useState, type FormEvent, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { SegmentGroup, SegmentGroupItem } from "darkraise-ui/components/segment-group"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "darkraise-ui/components/select"
import { GROUPS_QUERY_KEY, listGroups } from "@/api/client"
import type { AlertMetric, AlertRule, AlertSeverity } from "@/api/types"
import { METRICS, METRIC_LABELS, thresholdMax, thresholdUnit } from "@/lib/alerts"

const RULE_NAME = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,63}$/

type Errors = Partial<Record<"name" | "threshold" | "duration", string>>

function Field({ id, label, hint, error, children }: { id: string; label: string; hint?: string; error?: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {(error ?? hint) !== undefined && (
        <p id={`${id}-note`} className={error !== undefined ? "text-sm text-destructive-text" : "text-xs text-muted-foreground"}>
          {error ?? hint}
        </p>
      )}
    </div>
  )
}

function note(id: string, error: string | undefined, hint: boolean) {
  return { id, "aria-invalid": error !== undefined, "aria-describedby": error !== undefined || hint ? `${id}-note` : undefined }
}

// RuleForm creates a rule, or edits one when rule is set; onPlan gets the
// action's input. Offline ignores the comparator and threshold, so they are
// neither shown nor sent for it.
export function RuleForm({ rule, onCancel, onPlan }: { rule: AlertRule | null; onCancel: () => void; onPlan: (input: Record<string, unknown>) => void }) {
  const groups = useQuery({ queryKey: GROUPS_QUERY_KEY, queryFn: listGroups })
  const [name, setName] = useState(rule?.name ?? "")
  const [metric, setMetric] = useState<AlertMetric>(rule?.metric ?? "cpu")
  const [comparator, setComparator] = useState<">" | "<">(rule?.comparator ?? ">")
  const [threshold, setThreshold] = useState(rule === null ? "90" : String(rule.threshold))
  const [duration, setDuration] = useState(rule === null ? "10" : String(rule.durationMin))
  const [severity, setSeverity] = useState<AlertSeverity>(rule?.severity ?? "warning")
  const [target, setTarget] = useState(rule?.target ?? "*")
  const [errors, setErrors] = useState<Errors>({})

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const next: Errors = {}
    if (!RULE_NAME.test(name.trim())) next.name = "A name is 1 to 64 letters, digits, spaces, dots, underscores or hyphens, starting with a letter or digit."
    const limit = thresholdMax(metric)
    const level = Number(threshold)
    if (metric !== "offline" && (threshold.trim() === "" || !Number.isFinite(level) || level < 0 || level > limit)) next.threshold = `Enter 0 to ${limit}.`
    const minutes = Number(duration)
    if (!/^\d+$/.test(duration.trim()) || minutes < 1 || minutes > 1440) next.duration = "Enter whole minutes from 1 to 1440."
    setErrors(next)
    if (Object.keys(next).length > 0) return
    const input: Record<string, unknown> = { name: name.trim(), metric, durationMin: minutes, severity, target }
    if (metric !== "offline") {
      input.comparator = comparator
      input.threshold = level
    }
    onPlan(rule === null ? input : { id: rule.id, ...input, enabled: rule.enabled })
  }

  const side = comparator === "<" ? "Below" : "Above"
  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onCancel())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{rule === null ? "New rule" : `Edit ${rule.name}`}</DialogTitle>
          <DialogDescription>The rule fires when its condition holds on a host for the whole duration.</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
          <Field id="rule-name" label="Name" error={errors.name}>
            <Input {...note("rule-name", errors.name, false)} value={name} autoComplete="off" maxLength={64} onChange={(e) => setName(e.target.value)} />
          </Field>
          <div className="flex flex-col gap-2">
            <span id="rule-metric" className="text-sm font-medium">
              Metric
            </span>
            <SegmentGroup value={metric} onValueChange={(m) => setMetric(m as AlertMetric)} aria-labelledby="rule-metric" variant="outline" className="flex-wrap">
              {METRICS.map((m) => (
                <SegmentGroupItem key={m} value={m}>
                  {METRIC_LABELS[m]}
                </SegmentGroupItem>
              ))}
            </SegmentGroup>
            {metric === "offline" && <p className="text-xs text-muted-foreground">Fires when a host has no connection for the whole duration.</p>}
            {metric === "load" && <p className="text-xs text-muted-foreground">The 1-minute load divided by the host's logical CPUs.</p>}
            {metric === "disk" && <p className="text-xs text-muted-foreground">Checked on each mounted filesystem.</p>}
          </div>
          {metric !== "offline" && (
            <div className="flex flex-col gap-2">
              <span id="rule-comparator" className="text-sm font-medium">
                Fires when the figure is
              </span>
              <SegmentGroup value={comparator} onValueChange={(c) => setComparator(c as ">" | "<")} aria-labelledby="rule-comparator" variant="outline">
                <SegmentGroupItem value=">">Above</SegmentGroupItem>
                <SegmentGroupItem value="<">Below</SegmentGroupItem>
              </SegmentGroup>
            </div>
          )}
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {metric !== "offline" && (
              <Field id="rule-threshold" label={`${side} (${thresholdUnit(metric)})`} hint={`0 to ${thresholdMax(metric)}.`} error={errors.threshold}>
                <Input {...note("rule-threshold", errors.threshold, true)} value={threshold} inputMode="decimal" onChange={(e) => setThreshold(e.target.value)} />
              </Field>
            )}
            <Field id="rule-duration" label="For (minutes)" hint="1 to 1440." error={errors.duration}>
              <Input {...note("rule-duration", errors.duration, true)} value={duration} inputMode="numeric" onChange={(e) => setDuration(e.target.value)} />
            </Field>
          </div>
          <div className="flex flex-col gap-2">
            <span id="rule-severity" className="text-sm font-medium">
              Severity
            </span>
            <SegmentGroup value={severity} onValueChange={(s) => setSeverity(s as AlertSeverity)} aria-labelledby="rule-severity" variant="outline">
              <SegmentGroupItem value="warning">Warning</SegmentGroupItem>
              <SegmentGroupItem value="critical">Critical</SegmentGroupItem>
            </SegmentGroup>
          </div>
          <Field id="rule-target" label="Hosts" hint="All hosts, or one host group.">
            <Select value={target} onValueChange={setTarget}>
              <SelectTrigger id="rule-target" aria-describedby="rule-target-note" className="w-full min-w-0">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="*">All hosts</SelectItem>
                {(groups.data?.groups ?? []).map((g) => (
                  <SelectItem key={g.id} value={g.id} className="wrap-anywhere">
                    {`Group ${g.name}`}
                  </SelectItem>
                ))}
                {rule !== null && rule.target !== "*" && groups.data === undefined && <SelectItem value={rule.target}>{`Group ${rule.targetName}`}</SelectItem>}
              </SelectContent>
            </Select>
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onCancel}>
              Cancel
            </Button>
            <Button type="submit">{rule === null ? "Review rule" : "Review change"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
