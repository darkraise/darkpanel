import { useState, type RefObject } from "react"
import { ArrowDownLeft, ArrowLeftRight, ArrowUpRight, Check, X, type LucideIcon } from "lucide-react"
import { StatusPill } from "@/components/visual/status-pill"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { FirewallChange } from "./firewall-change"
import type { ChainDifference, FirewallStatus, Policy } from "./types"
import { Can } from "@/session/can"

const POLICY_WORDS: Record<Policy, string> = { ACCEPT: "allow", DROP: "deny", REJECT: "reject" }

function policyText(p: Policy): string {
  return POLICY_WORDS[p] ?? p
}

function yesNo(v: boolean): string {
  return v ? "Yes" : "No"
}

function chainName(d: ChainDifference): string {
  return `${d.family} ${d.table} ${d.chain}`
}

// StatusBanners says why changes are refused: an unsupported configuration
// refuses every change; a host out of sync refuses every change but a
// repair, which it offers. fallbackFocus takes focus when the repair dialog
// closes after a repair that succeeded has taken its Repair button away.
export function StatusBanners({ hostId, status, fallbackFocus }: {
  hostId: string
  status: FirewallStatus
  fallbackFocus?: RefObject<HTMLElement | null>
}) {
  const [repairing, setRepairing] = useState(false)
  return (
    <>
      {!status.supported && (
        <Alert variant="destructive" role="alert">
          <AlertTitle>Unsupported configuration</AlertTitle>
          <AlertDescription>
            <p>UFW's load on this host is unpredictable, so every firewall change is refused until fixed:</p>
            <ul aria-label="Unsupported configuration reasons" className="mt-1 list-disc pl-5">
              {status.unsupported.map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {status.supported && !status.inSync && (
        <Alert variant="warning" role="status">
          <AlertTitle>Out of sync</AlertTitle>
          <AlertDescription>
            {status.syncError !== undefined && status.syncError !== "" ? (
              <p>{`Could not check the loaded rules against UFW's files, so only a repair is allowed: ${status.syncError}`}</p>
            ) : (
              <>
                <p>The loaded rules differ from UFW&apos;s files, so only a repair is allowed. Differing chains:</p>
                <ul aria-label="Differing chains" className="mt-1 list-disc pl-5 font-mono text-xs">
                  {status.outOfSync.map((d) => (
                    <li key={chainName(d)}>{chainName(d)}</li>
                  ))}
                </ul>
              </>
            )}
            <Can need="operate"><Button className="mt-2" size="sm" variant="outline" onClick={() => setRepairing(true)}>
              Repair
            </Button></Can>
          </AlertDescription>
        </Alert>
      )}
      <FirewallChange
        hostId={hostId}
        action="firewall.state.repair"
        title="Repair the firewall"
        input={repairing ? {} : null}
        onClose={() => setRepairing(false)}
        fallbackFocus={fallbackFocus}
      />
    </>
  )
}

const DIRECTIONS = [
  { key: "incoming", label: "Inbound", icon: ArrowDownLeft },
  { key: "outgoing", label: "Outbound", icon: ArrowUpRight },
  { key: "routed", label: "Routed", icon: ArrowLeftRight },
] as const

// PolicyArrow draws one default policy: the direction as an arrow, allow or
// deny as a mark beside it, and both in its name.
function PolicyArrow({ label, icon: Arrow, policy }: { label: string; icon: LucideIcon; policy: Policy }) {
  const allow = policy === "ACCEPT"
  const Mark = allow ? Check : X
  return (
    <div role="img" aria-label={`${label}: ${policyText(policy)}`} className="flex items-center gap-2 text-sm">
      <Arrow aria-hidden className="text-muted-foreground size-5" />
      <span>{label}</span>
      <Mark aria-hidden className={allow ? "text-success-text size-4" : "text-destructive-text size-4"} />
      <span className="text-muted-foreground">{policyText(policy)}</span>
    </div>
  )
}

function Chip({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return <Badge variant="outline" aria-label={`${label}: ${value}`} className={mono ? "font-mono" : undefined}>{`${label}: ${value}`}</Badge>
}

// StatusSummary shows the configured state from UFW's files beside the
// loaded state the kernel holds.
export function StatusSummary({ status }: { status: FirewallStatus }) {
  const c = status.configured
  const l = status.loaded
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <Card role="region" aria-label="Configured state">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            Configured
            <StatusPill status={c.enabled ? "ok" : "off"} label={c.enabled ? "Enabled at boot" : "Not enabled at boot"} />
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {DIRECTIONS.map((d) => (
            <PolicyArrow key={d.key} label={d.label} icon={d.icon} policy={c.defaults[d.key]} />
          ))}
          <div className="flex flex-wrap gap-1">
            <Chip label="Logging" value={c.logLevel} />
            <Chip label="IPv6" value={yesNo(c.ipv6)} />
          </div>
        </CardContent>
      </Card>
      <Card role="region" aria-label="Loaded state">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            Loaded
            <StatusPill status={status.active ? "ok" : "off"} label={status.active ? "Active" : "Inactive"} />
            <StatusPill status={status.inSync ? "ok" : "warn"} label={status.inSync ? "In sync" : "Out of sync"} />
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-wrap gap-1">
          <Chip label="iptables variant" value={l.variant} mono />
          <Chip label="IPv6 rules" value={l.ipv6 ? "Read" : "No ip6tables"} />
          <Chip label="Chains loaded" value={String(l.chains.length)} />
          {Object.keys(l.sysctl)
            .sort()
            .map((key) => (
              <Chip key={key} label={key} value={l.sysctl[key] ?? ""} mono />
            ))}
          {(status.neighbours ?? []).map((n) => (
            <p key={n.kind} className="w-full text-sm text-muted-foreground">
              {`${n.kind}: ${n.banned} banned ${n.banned === 1 ? "address" : "addresses"}`}
            </p>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}
