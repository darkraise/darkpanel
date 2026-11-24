import type { AlertMetric, AlertRule, FiredAlert } from "@/api/types"

export const ALERTS_QUERY_KEY = ["alerts"] as const
export const ALERT_RULES_QUERY_KEY = ["alert-rules"] as const

export const METRICS: AlertMetric[] = ["cpu", "memory", "swap", "disk", "load", "offline"]

export const METRIC_LABELS: Record<AlertMetric, string> = {
  cpu: "CPU",
  memory: "Memory",
  swap: "Swap",
  disk: "Disk",
  load: "Load",
  offline: "Offline",
}

export const SEVERITY_LABELS = { warning: "Warning", critical: "Critical" } as const

// thresholdMax is the highest threshold the controller accepts: a percent
// for every metric but load, which is per logical CPU.
export function thresholdMax(metric: AlertMetric): number {
  return metric === "load" ? 1000 : 100
}

export function thresholdUnit(metric: AlertMetric): string {
  return metric === "load" ? "per CPU" : "%"
}

function figure(n: number): string {
  return Number.isInteger(n) ? String(n) : n.toFixed(1)
}

export function minutesText(n: number): string {
  return n === 1 ? "1 minute" : `${n} minutes`
}

export function conditionText(rule: Pick<AlertRule, "metric" | "comparator" | "threshold">): string {
  if (rule.metric === "offline") return "Offline"
  const side = rule.comparator === "<" ? "below" : "above"
  return `${METRIC_LABELS[rule.metric]} ${side} ${figure(rule.threshold)} ${thresholdUnit(rule.metric)}`
}

export function targetText(rule: Pick<AlertRule, "target" | "targetName">): string {
  return rule.target === "*" ? "All hosts" : `Group ${rule.targetName}`
}

// valueText is the figure an alert carries: a percent, load per logical
// CPU, or, for offline, the minutes without a connection.
export function valueText(alert: Pick<FiredAlert, "metric" | "value">): string {
  if (alert.metric === "offline") return `No connection for ${minutesText(Math.round(alert.value))}`
  return `${alert.value.toFixed(1)} ${thresholdUnit(alert.metric)}`
}

export function alertTitle(alert: Pick<FiredAlert, "ruleName" | "subject">): string {
  return alert.subject === "" ? alert.ruleName : `${alert.ruleName} on ${alert.subject}`
}
