import { useState } from "react"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "darkraise-ui/components/table"
import { FirewallChange } from "./firewall-change"
import type { Rule } from "./types"

// endpoint is a rule's source or destination with its port, as UFW shows it.
function endpoint(addr: string, port: string | undefined): string {
  return port === undefined || port === "" ? addr : `${addr} port ${port}`
}

function iface(r: Rule): string {
  const parts: string[] = []
  if (r.inIface) parts.push(`in on ${r.inIface}`)
  if (r.outIface) parts.push(`out on ${r.outIface}`)
  return parts.join(", ")
}

// ruleSummary is one line naming what a rule does, for titles and labels.
function ruleSummary(r: Rule): string {
  return [r.action, r.direction, r.proto, "from", endpoint(r.from, r.fromPort), "to", endpoint(r.to, r.toPort), iface(r)]
    .filter((p) => p !== "")
    .join(" ")
}

function OriginBadge({ rule }: { rule: Rule }) {
  if (rule.raw !== undefined && rule.raw !== "") return <Badge variant="outline" size="sm">read-only</Badge>
  if (rule.origin === "manual") return <Badge variant="secondary" size="sm">manual</Badge>
  return <Badge size="sm">darkpanel</Badge>
}

// ManualConsent asks before a manual rule is changed: Darkpanel did not
// make it, and the action refuses it unless the input says manual: true.
function ManualConsent({ rule, onCancel, onConfirm }: { rule: Rule | null; onCancel: () => void; onConfirm: () => void }) {
  return (
    <Dialog open={rule !== null} onOpenChange={(open) => !open && onCancel()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Delete a manual rule?</DialogTitle>
          <DialogDescription>
            {`Darkpanel did not make the rule "${rule === null ? "" : ruleSummary(rule)}"; someone added it by hand. Deleting it may cut access that rule exists for.`}
          </DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={onConfirm}>
            Delete it anyway
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

// RulesTable lists UFW's rules in file order with who made each. A rule
// the model cannot express is shown read-only with its raw tuple; every
// other rule can be deleted through the change dialog.
export function RulesTable({ hostId, rules }: { hostId: string; rules: Rule[] }) {
  const [asking, setAsking] = useState<Rule | null>(null)
  const [deleting, setDeleting] = useState<{ rule: Rule; manual: boolean } | null>(null)

  function startDelete(rule: Rule) {
    if (rule.origin === "manual") setAsking(rule)
    else setDeleting({ rule, manual: false })
  }

  const input = deleting === null ? null : deleting.manual ? { id: deleting.rule.id, manual: true } : { id: deleting.rule.id }
  return (
    <>
      {rules.length === 0 ? (
        <EmptyState title="No rules" description="UFW has no rules on this host." />
      ) : (
      <Table aria-label="Firewall rules">
        <TableHeader>
          <TableRow>
            <TableHead>#</TableHead>
            <TableHead>Action</TableHead>
            <TableHead>Direction</TableHead>
            <TableHead>Protocol</TableHead>
            <TableHead>From</TableHead>
            <TableHead>To</TableHead>
            <TableHead>Interface</TableHead>
            <TableHead>Family</TableHead>
            <TableHead>Log</TableHead>
            <TableHead>Comment</TableHead>
            <TableHead>Origin</TableHead>
            <TableHead>
              <span className="sr-only">Actions</span>
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rules.map((r, i) => {
            const readOnly = r.raw !== undefined && r.raw !== ""
            return (
              <TableRow key={r.id ?? `row-${i}`}>
                <TableCell>{i + 1}</TableCell>
                {readOnly ? (
                  <TableCell colSpan={9}>
                    <code className="font-mono text-xs">{r.raw}</code>
                  </TableCell>
                ) : (
                  <>
                    <TableCell className="font-medium">{r.action}</TableCell>
                    <TableCell>{r.direction}</TableCell>
                    <TableCell>{r.proto}</TableCell>
                    <TableCell className="font-mono">{endpoint(r.from, r.fromPort)}</TableCell>
                    <TableCell className="font-mono">{endpoint(r.to, r.toPort)}</TableCell>
                    <TableCell className="font-mono">{iface(r)}</TableCell>
                    <TableCell>{r.family}</TableCell>
                    <TableCell>{r.log ?? "off"}</TableCell>
                    <TableCell>{r.comment ?? ""}</TableCell>
                  </>
                )}
                <TableCell>
                  <OriginBadge rule={r} />
                </TableCell>
                <TableCell>
                  {!readOnly && r.id !== undefined && (
                    <Button variant="outline" size="sm" aria-label={`Delete rule ${i + 1}`} onClick={() => startDelete(r)}>
                      Delete
                    </Button>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      )}
      <ManualConsent
        rule={asking}
        onCancel={() => setAsking(null)}
        onConfirm={() => {
          if (asking !== null) setDeleting({ rule: asking, manual: true })
          setAsking(null)
        }}
      />
      <FirewallChange
        hostId={hostId}
        action="firewall.rule.delete"
        title={deleting === null ? "Delete a rule" : `Delete rule: ${ruleSummary(deleting.rule)}`}
        input={input}
        onClose={() => setDeleting(null)}
      />
    </>
  )
}
