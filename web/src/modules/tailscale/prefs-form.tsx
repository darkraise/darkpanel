import { useState, type FormEvent } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { TailscaleChange } from "./tailscale-change"
import type { PrefsFields, TailscaleStatus } from "./types"

type BoolField = "wantRunning" | "acceptRoutes" | "shieldsUp" | "runSSH" | "advertiseExitNode"

interface Values {
  wantRunning: boolean
  acceptRoutes: boolean
  shieldsUp: boolean
  runSSH: boolean
  advertiseExitNode: boolean
  exitNode: string
  routes: string // the advertised routes as typed
}

const BOOLEANS: { field: BoolField; label: string }[] = [
  { field: "wantRunning", label: "Keep Tailscale running" },
  { field: "acceptRoutes", label: "Accept subnet routes" },
  { field: "shieldsUp", label: "Shields up" },
  { field: "runSSH", label: "Tailscale SSH" },
  { field: "advertiseExitNode", label: "Advertise as an exit node" },
]

const DEFAULT_ROUTES = ["0.0.0.0/0", "::/0"]

function parseRoutes(text: string): string[] {
  return text
    .split(",")
    .map((route) => route.trim())
    .filter((route) => route !== "")
}

function sameSet(a: string[], b: string[]): boolean {
  const setA = new Set(a)
  const setB = new Set(b)
  return setA.size === setB.size && [...setA].every((route) => setB.has(route))
}

function nodeValues(status: TailscaleStatus, prefs: PrefsFields): Values {
  return {
    wantRunning: prefs.wantRunning ?? status.backendState !== "Stopped",
    acceptRoutes: prefs.acceptRoutes ?? false,
    shieldsUp: prefs.shieldsUp ?? false,
    runSSH: prefs.runSSH ?? status.runSSH,
    advertiseExitNode: prefs.advertiseExitNode ?? false,
    exitNode: prefs.exitNode ?? status.peers.find((peer) => peer.exitNode)?.id ?? "",
    routes: (prefs.advertiseRoutes ?? []).join(", "),
  }
}

// PrefsForm edits the node's preferences and opens the change for the fields
// that differ from the node's values. The draft holds only what the user
// edited, so a refreshed status shows through everywhere else.
export function PrefsForm({ hostId, status, prefs }: { hostId: string; status: TailscaleStatus; prefs: PrefsFields }) {
  const [draft, setDraft] = useState<Partial<Values>>({})
  const [pending, setPending] = useState<Record<string, unknown> | null>(null)
  const loggedIn = status.self !== null
  const node = nodeValues(status, prefs)
  const shown: Values = { ...node, ...draft }

  const routes = parseRoutes(shown.routes)
  const defaultRoute = routes.some((route) => DEFAULT_ROUTES.includes(route))

  const input: Record<string, unknown> = {}
  for (const { field } of BOOLEANS) {
    if (shown[field] !== node[field]) input[field] = shown[field]
  }
  if (shown.exitNode.trim() !== node.exitNode) input.exitNode = shown.exitNode.trim()
  if (!sameSet(routes, parseRoutes(node.routes))) input.advertiseRoutes = routes
  const canApply = loggedIn && !defaultRoute && Object.keys(input).length > 0

  function edit<K extends keyof Values>(field: K, value: Values[K]) {
    setDraft((d) => ({ ...d, [field]: value }))
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (canApply) setPending(input)
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Preferences</CardTitle>
      </CardHeader>
      <CardContent>
        <form aria-label="Tailscale preferences" className="flex flex-col gap-4" onSubmit={submit}>
          {!loggedIn && <p className="text-sm text-muted-foreground">Log the node in to change its preferences.</p>}
          {BOOLEANS.map(({ field, label }) => (
            <div key={field} className="flex items-center gap-2">
              <Checkbox
                id={`tailscale-pref-${field}`}
                checked={shown[field]}
                disabled={!loggedIn}
                onCheckedChange={(checked) => edit(field, checked === true)}
              />
              <Label htmlFor={`tailscale-pref-${field}`}>{label}</Label>
            </div>
          ))}
          <div className="flex flex-col gap-2">
            <Label htmlFor="tailscale-pref-exit-node">Exit node</Label>
            <Input
              id="tailscale-pref-exit-node"
              value={shown.exitNode}
              placeholder="node ID or Tailscale IP; empty for none"
              autoComplete="off"
              disabled={!loggedIn}
              onChange={(e) => edit("exitNode", e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="tailscale-pref-routes">Advertised routes (comma-separated)</Label>
            <Input
              id="tailscale-pref-routes"
              value={shown.routes}
              placeholder="10.0.0.0/24, 192.168.1.0/24"
              autoComplete="off"
              disabled={!loggedIn}
              aria-invalid={defaultRoute ? true : undefined}
              aria-describedby={defaultRoute ? "tailscale-pref-routes-error" : undefined}
              onChange={(e) => edit("routes", e.target.value)}
            />
            {defaultRoute && (
              <p id="tailscale-pref-routes-error" className="text-sm text-destructive">
                Use the exit node option to advertise a default route.
              </p>
            )}
          </div>
          <div>
            <Button type="submit" disabled={!canApply}>
              Apply preferences
            </Button>
          </div>
        </form>
      </CardContent>
      {pending !== null && (
        <TailscaleChange
          hostId={hostId}
          action="tailscale.prefs.set"
          title="Change the Tailscale preferences"
          input={pending}
          onClose={() => {
            setPending(null)
            setDraft({})
          }}
        />
      )}
    </Card>
  )
}
