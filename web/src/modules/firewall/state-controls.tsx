import { useState, type FormEvent } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Label } from "darkraise-ui/components/label"
import { RadioGroup, RadioGroupItem } from "darkraise-ui/components/radio-group"
import { FirewallChange } from "./firewall-change"
import type { FirewallStatus, Policy } from "./types"

type PolicyDirection = "incoming" | "outgoing" | "routed"
type PolicyWord = "allow" | "deny" | "reject"

const DIRECTIONS: PolicyDirection[] = ["incoming", "outgoing", "routed"]
const POLICIES: PolicyWord[] = ["allow", "deny", "reject"]
const LEVELS = ["off", "low", "medium", "high", "full"]
const WORD_OF: Record<Policy, PolicyWord> = { ACCEPT: "allow", DROP: "deny", REJECT: "reject" }
const POLICY_OF: Record<PolicyWord, Policy> = { allow: "ACCEPT", deny: "DROP", reject: "REJECT" }

function currentPolicy(s: FirewallStatus, d: PolicyDirection): PolicyWord {
  return WORD_OF[s.configured.defaults[d]] ?? "deny"
}

function Choice({ name, label, options, value, onChange }: {
  name: string
  label: string
  options: string[]
  value: string
  onChange: (value: string) => void
}) {
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="text-sm font-medium">{label}</legend>
      <RadioGroup value={value} onValueChange={onChange} aria-label={label} className="flex flex-wrap gap-4">
        {options.map((o) => (
          <div key={o} className="flex items-center gap-2">
            <RadioGroupItem id={`fw-${name}-${o}`} value={o} />
            <Label htmlFor={`fw-${name}-${o}`}>{o}</Label>
          </div>
        ))}
      </RadioGroup>
    </fieldset>
  )
}

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

// StateControls enables or disables UFW and sets its default policies and
// its logging level, each through the change dialog.
export function StateControls({ hostId, status }: { hostId: string; status: FirewallStatus }) {
  const [pending, setPending] = useState<Pending | null>(null)
  const [direction, setDirection] = useState<PolicyDirection>("incoming")
  const [policy, setPolicy] = useState<PolicyWord>(() => currentPolicy(status, "incoming"))
  const [level, setLevel] = useState(status.configured.logLevel)

  function chooseDirection(d: PolicyDirection) {
    setDirection(d)
    setPolicy(currentPolicy(status, d))
  }

  function setDefault(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending({ action: "firewall.defaults.set", title: `Set the default ${direction} policy to ${policy}`, input: { [direction]: POLICY_OF[policy] } })
  }

  function setLogging(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setPending({ action: "firewall.logging.set", title: `Set logging to ${level}`, input: { level } })
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Settings</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <div className="flex items-center gap-4">
          <span>{status.active ? "UFW is active." : "UFW is inactive."}</span>
          {status.active ? (
            <Button variant="outline" onClick={() => setPending({ action: "firewall.state.disable", title: "Disable the firewall", input: {} })}>
              Disable
            </Button>
          ) : (
            <Button onClick={() => setPending({ action: "firewall.state.enable", title: "Enable the firewall", input: {} })}>Enable</Button>
          )}
        </div>
        <form aria-label="Default policy" className="flex flex-col gap-3" onSubmit={setDefault}>
          <Choice name="direction" label="Default policy for" options={DIRECTIONS} value={direction} onChange={(v) => chooseDirection(v as PolicyDirection)} />
          <Choice name="policy" label="Policy" options={POLICIES} value={policy} onChange={(v) => setPolicy(v as PolicyWord)} />
          <div>
            <Button type="submit" variant="outline" disabled={policy === currentPolicy(status, direction)}>
              Set default policy
            </Button>
          </div>
        </form>
        <form aria-label="Logging" className="flex flex-col gap-3" onSubmit={setLogging}>
          <Choice name="level" label="Logging level" options={LEVELS} value={level} onChange={setLevel} />
          <div>
            <Button type="submit" variant="outline" disabled={level === status.configured.logLevel}>
              Set logging
            </Button>
          </div>
        </form>
      </CardContent>
      <FirewallChange
        hostId={hostId}
        action={pending?.action ?? "firewall.state.enable"}
        title={pending?.title ?? ""}
        input={pending?.input ?? null}
        onClose={() => setPending(null)}
      />
    </Card>
  )
}
