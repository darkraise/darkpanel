import { useState, type FormEvent } from "react"
import { Button } from "darkraise-ui/components/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { FirewallChange } from "./firewall-change"
import type { Rule, RuleAction, RuleDirection, RuleFamily, RuleLog, RuleProto } from "./types"

const ACTIONS: RuleAction[] = ["allow", "deny", "limit", "reject"]
const DIRECTIONS: RuleDirection[] = ["in", "out", "route"]
const PROTOS: RuleProto[] = ["tcp", "udp", "any", "ah", "esp", "gre", "ipv6", "igmp"]
const FAMILIES: RuleFamily[] = ["both", "v4", "v6"]
const LOGS: RuleLog[] = ["off", "log", "log-all"]

// Choice is one enumeration of the rule model as a radio group.
function Choice<T extends string>({ name, label, options, value, disabled, onChange }: {
  name: string
  label: string
  options: T[]
  value: T
  disabled?: (option: T) => boolean
  onChange: (value: T) => void
}) {
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="text-sm font-medium">{label}</legend>
      <RadioGroup value={value} onValueChange={(v) => onChange(v as T)} aria-label={label} className="flex flex-wrap gap-4">
        {options.map((o) => (
          <div key={o} className="flex items-center gap-2">
            <RadioGroupItem id={`rule-${name}-${o}`} value={o} disabled={disabled?.(o) ?? false} />
            <Label htmlFor={`rule-${name}-${o}`}>{o}</Label>
          </div>
        ))}
      </RadioGroup>
    </fieldset>
  )
}

function Text({ id, label, value, placeholder, disabled, onChange }: {
  id: string
  label: string
  value: string
  placeholder?: string
  disabled?: boolean
  onChange: (value: string) => void
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} value={value} placeholder={placeholder} disabled={disabled} autoComplete="off" onChange={(e) => onChange(e.target.value)} />
    </div>
  )
}

interface Draft {
  action: RuleAction
  direction: RuleDirection
  proto: RuleProto
  from: string
  fromPort: string
  to: string
  toPort: string
  inIface: string
  outIface: string
  family: RuleFamily
  log: RuleLog
  comment: string
}

const EMPTY: Draft = {
  action: "allow", direction: "in", proto: "tcp", from: "any", fromPort: "", to: "any", toPort: "",
  inIface: "", outIface: "", family: "both", log: "off", comment: "",
}

const PORTLESS = new Set<RuleProto>(["ah", "esp", "gre", "ipv6", "igmp"])

// ruleOf is the rule a draft describes. A field that does not apply to the
// direction or protocol, and an empty optional field, is left out; the
// action checks everything else against UFW's grammar (spec 2.2).
function ruleOf(d: Draft): Rule {
  const rule: Rule = { action: d.action, direction: d.direction, proto: d.proto, from: d.from.trim(), to: d.to.trim(), family: d.family }
  if (!PORTLESS.has(d.proto)) {
    if (d.fromPort.trim() !== "") rule.fromPort = d.fromPort.trim()
    if (d.toPort.trim() !== "") rule.toPort = d.toPort.trim()
  }
  if (d.direction !== "out" && d.inIface.trim() !== "") rule.inIface = d.inIface.trim()
  if (d.direction !== "in" && d.outIface.trim() !== "") rule.outIface = d.outIface.trim()
  if (d.log !== "off") rule.log = d.log
  if (d.comment.trim() !== "") rule.comment = d.comment.trim()
  return rule
}

// AddRule collects one rule of the model and plans firewall.rule.add with
// it through the change dialog.
export function AddRule({ hostId }: { hostId: string }) {
  const [open, setOpen] = useState(false)
  const [draft, setDraft] = useState<Draft>(EMPTY)
  const [planned, setPlanned] = useState<Rule | null>(null)

  function set<K extends keyof Draft>(key: K, value: Draft[K]) {
    setDraft((d) => {
      const next = { ...d, [key]: value }
      if (next.action === "limit" && next.direction !== "in") next.action = "allow"
      return next
    })
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPlanned(ruleOf(draft))
    setOpen(false)
  }

  const portless = PORTLESS.has(draft.proto)
  return (
    <>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>
          <Button>Add rule</Button>
        </DialogTrigger>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Add a rule</DialogTitle>
            <DialogDescription>The rule is tagged as Darkpanel&apos;s. The next step shows what it changes before anything is applied.</DialogDescription>
          </DialogHeader>
          <form aria-label="Rule" className="flex flex-col gap-4" onSubmit={submit}>
            <Choice name="action" label="Action" options={ACTIONS} value={draft.action}
              disabled={(o) => o === "limit" && draft.direction !== "in"} onChange={(v) => set("action", v)} />
            <Choice name="direction" label="Direction" options={DIRECTIONS} value={draft.direction} onChange={(v) => set("direction", v)} />
            <Choice name="proto" label="Protocol" options={PROTOS} value={draft.proto} onChange={(v) => set("proto", v)} />
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Text id="rule-from" label="From" value={draft.from} placeholder="any, an address or a CIDR" onChange={(v) => set("from", v)} />
              <Text id="rule-from-port" label="From port" value={draft.fromPort} placeholder="22, 1000:2000 or 80,443" disabled={portless} onChange={(v) => set("fromPort", v)} />
              <Text id="rule-to" label="To" value={draft.to} placeholder="any, an address or a CIDR" onChange={(v) => set("to", v)} />
              <Text id="rule-to-port" label="To port" value={draft.toPort} placeholder="22, 1000:2000 or 80,443" disabled={portless} onChange={(v) => set("toPort", v)} />
              <Text id="rule-in-iface" label="In on interface" value={draft.inIface} disabled={draft.direction === "out"} onChange={(v) => set("inIface", v)} />
              <Text id="rule-out-iface" label="Out on interface" value={draft.outIface} disabled={draft.direction === "in"} onChange={(v) => set("outIface", v)} />
            </div>
            <Choice name="family" label="Family" options={FAMILIES} value={draft.family} onChange={(v) => set("family", v)} />
            <Choice name="log" label="Logging" options={LOGS} value={draft.log} onChange={(v) => set("log", v)} />
            <Text id="rule-comment" label="Comment" value={draft.comment} onChange={(v) => set("comment", v)} />
            <DialogFooter>
              <Button type="submit">Plan</Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
      <FirewallChange
        hostId={hostId}
        action="firewall.rule.add"
        title="Add a rule"
        input={planned === null ? null : { rule: planned }}
        onClose={() => {
          setPlanned(null)
          setDraft(EMPTY)
        }}
      />
    </>
  )
}
