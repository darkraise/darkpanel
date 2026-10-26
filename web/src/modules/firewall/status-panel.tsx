import { useState } from "react"
import { Alert, AlertDescription, AlertTitle } from "darkraise-ui/components/alert"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { FirewallChange } from "./firewall-change"
import type { ChainDifference, FirewallStatus, Policy } from "./types"

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
// repair, which it offers.
export function StatusBanners({ hostId, status }: { hostId: string; status: FirewallStatus }) {
  const [repairing, setRepairing] = useState(false)
  return (
    <>
      {!status.supported && (
        <Alert variant="destructive" role="alert">
          <AlertTitle>Unsupported configuration</AlertTitle>
          <AlertDescription>
            <p>Darkpanel cannot predict what UFW will load on this host, so it refuses every firewall change until this is fixed:</p>
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
              <p>{`Whether the loaded rules match UFW's files could not be checked, so every change except a repair is refused: ${status.syncError}`}</p>
            ) : (
              <>
                <p>The loaded rules differ from what UFW&apos;s files describe, so every change except a repair is refused. Differing chains:</p>
                <ul aria-label="Differing chains" className="mt-1 list-disc pl-5 font-mono text-xs">
                  {status.outOfSync.map((d) => (
                    <li key={chainName(d)}>{chainName(d)}</li>
                  ))}
                </ul>
              </>
            )}
            <Button className="mt-2" size="sm" variant="outline" onClick={() => setRepairing(true)}>
              Repair
            </Button>
          </AlertDescription>
        </Alert>
      )}
      <FirewallChange
        hostId={hostId}
        action="firewall.state.repair"
        title="Repair the firewall"
        input={repairing ? {} : null}
        onClose={() => setRepairing(false)}
      />
    </>
  )
}

interface Row {
  label: string
  value: string
  mono?: boolean
}

function configuredRows(s: FirewallStatus): Row[] {
  const c = s.configured
  return [
    { label: "Enabled at boot", value: yesNo(c.enabled) },
    { label: "Incoming", value: policyText(c.defaults.incoming) },
    { label: "Outgoing", value: policyText(c.defaults.outgoing) },
    { label: "Routed", value: policyText(c.defaults.routed) },
    { label: "Logging", value: c.logLevel },
    { label: "IPv6", value: yesNo(c.ipv6) },
  ]
}

function loadedRows(s: FirewallStatus): Row[] {
  const rows: Row[] = [
    { label: "iptables variant", value: s.loaded.variant, mono: true },
    { label: "IPv6 rules", value: s.loaded.ipv6 ? "Read" : "No ip6tables" },
    { label: "Chains loaded", value: String(s.loaded.chains.length) },
    { label: "In sync", value: yesNo(s.inSync) },
  ]
  for (const key of Object.keys(s.loaded.sysctl).sort()) {
    rows.push({ label: key, value: s.loaded.sysctl[key] ?? "", mono: true })
  }
  return rows
}

function Rows({ label, rows }: { label: string; rows: Row[] }) {
  return (
    <dl aria-label={label} className="grid grid-cols-1 gap-x-8 gap-y-2 sm:grid-cols-[max-content_1fr]">
      {rows.map((row) => (
        <div key={row.label} className="contents">
          <dt className={row.mono === true ? "font-mono text-sm text-muted-foreground" : "text-muted-foreground"}>{row.label}</dt>
          <dd className={row.mono === true ? "font-mono" : undefined}>{row.value}</dd>
        </div>
      ))}
    </dl>
  )
}

// StatusSummary shows the configured state from UFW's files beside the
// loaded state the kernel holds.
export function StatusSummary({ status }: { status: FirewallStatus }) {
  return (
    <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Configured</CardTitle>
        </CardHeader>
        <CardContent>
          <Rows label="Configured state" rows={configuredRows(status)} />
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            Loaded
            <Badge variant={status.active ? "default" : "secondary"} size="sm">
              {status.active ? "Active" : "Inactive"}
            </Badge>
          </CardTitle>
        </CardHeader>
        <CardContent>
          <Rows label="Loaded state" rows={loadedRows(status)} />
        </CardContent>
      </Card>
    </div>
  )
}
