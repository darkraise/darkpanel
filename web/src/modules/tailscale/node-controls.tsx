import { useState, type FormEvent } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { TailscaleChange } from "./tailscale-change"
import type { TailscaleStatus } from "./types"

interface Pending {
  action: string
  title: string
  input: Record<string, unknown>
}

function parseTags(text: string): string[] {
  const tags: string[] = []
  for (const part of text.split(",")) {
    const tag = part.trim()
    if (tag !== "" && !tags.includes(tag)) tags.push(tag)
  }
  return tags
}

// NodeControls logs the node in with an auth key and logs it out, through the
// change dialog. The key is cleared as soon as the change is opened, so it
// lives only in the open change's input.
export function NodeControls({ hostId, status }: { hostId: string; status: TailscaleStatus }) {
  const loggedIn = status.self !== null
  const [authKey, setAuthKey] = useState("")
  const [hostname, setHostname] = useState("")
  const [tags, setTags] = useState("")
  const [pending, setPending] = useState<Pending | null>(null)

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const key = authKey.trim()
    if (key === "") return
    const input: Record<string, unknown> = { authKey: key }
    if (hostname.trim() !== "") input.hostname = hostname.trim()
    const advertiseTags = parseTags(tags)
    if (advertiseTags.length > 0) input.advertiseTags = advertiseTags
    if (loggedIn) input.relogin = true
    setPending({
      action: "tailscale.node.login",
      title: loggedIn ? "Log the node in again" : "Log the node in to the tailnet",
      input,
    })
    setAuthKey("")
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Node</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        <form aria-label="Log in" className="flex flex-col gap-4" onSubmit={submit}>
          <div className="flex flex-col gap-2">
            <Label htmlFor="tailscale-auth-key">Auth key</Label>
            <Input
              id="tailscale-auth-key"
              type="password"
              autoComplete="off"
              value={authKey}
              onChange={(e) => setAuthKey(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="tailscale-hostname">Hostname (optional)</Label>
            <Input id="tailscale-hostname" value={hostname} onChange={(e) => setHostname(e.target.value)} />
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="tailscale-tags">Tags (comma-separated)</Label>
            <Input id="tailscale-tags" value={tags} onChange={(e) => setTags(e.target.value)} />
          </div>
          <div>
            <Button type="submit" disabled={authKey.trim() === ""}>
              {loggedIn ? "Log in again" : "Log in"}
            </Button>
          </div>
        </form>
        {loggedIn && (
          <div>
            <Button
              variant="outline"
              onClick={() => setPending({ action: "tailscale.node.logout", title: "Log the node out of the tailnet", input: {} })}
            >
              Log out
            </Button>
          </div>
        )}
      </CardContent>
      {pending !== null && (
        <TailscaleChange hostId={hostId} action={pending.action} title={pending.title} input={pending.input} onClose={() => setPending(null)} />
      )}
    </Card>
  )
}
