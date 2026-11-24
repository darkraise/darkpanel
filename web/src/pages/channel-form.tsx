import { useState, type FormEvent, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { Button } from "darkraise-ui/components/button"
import { Checkbox } from "darkraise-ui/components/checkbox"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Label } from "darkraise-ui/components/label"
import { SegmentGroup, SegmentGroupIndicator, SegmentGroupItem } from "darkraise-ui/components/segment-group"
import { Switch } from "darkraise-ui/components/switch"
import { Textarea } from "darkraise-ui/components/textarea"
import { GROUPS_QUERY_KEY, listGroups } from "@/api/client"
import type { ChannelKind, NotifyChannel, Severity } from "@/api/types"
import { EVENT_GROUPS, KIND_LABELS } from "@/lib/notify"

const NAME = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,47}$/
const TOPIC = /^[A-Za-z0-9_-]{1,64}$/
const CHAT_ID = /^(-?[0-9]+|@[A-Za-z0-9_]{1,64})$/
const SAVED = "Saved — enter a new value to replace it."

type Errors = Record<string, string>

function urlWith(text: string, schemes: string[]): URL | null {
  try {
    const u = new URL(text)
    return schemes.includes(u.protocol) && u.host !== "" ? u : null
  } catch {
    return null
  }
}

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

// note wires an input to its field's hint or error.
function note(id: string, errors: Errors, key: string, hint: boolean) {
  const bad = errors[key] !== undefined
  return { id, "aria-invalid": bad, "aria-describedby": bad || hint ? `${id}-note` : undefined }
}

function Ticks({ legend, hint, items, value, onChange }: { legend: string; hint: string; items: { id: string; label: string }[]; value: Set<string>; onChange: (v: Set<string>) => void }) {
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-1 text-sm font-medium">{legend}</legend>
      <p className="text-xs text-muted-foreground">{hint}</p>
      <div className="relative flex max-h-40 flex-col gap-2 overflow-y-auto">
        {items.map((it) => (
          <div key={it.id} className="flex min-w-0 items-center gap-2 text-sm">
            <Checkbox
              id={`tick-${legend}-${it.id}`}
              checked={value.has(it.id)}
              onCheckedChange={(on) => {
                const next = new Set(value)
                if (on === true) next.add(it.id)
                else next.delete(it.id)
                onChange(next)
              }}
            />
            <Label htmlFor={`tick-${legend}-${it.id}`} className="min-w-0 font-normal wrap-anywhere">
              {it.label}
            </Label>
          </div>
        ))}
      </div>
    </fieldset>
  )
}

// ChannelForm collects a channel's settings for notify.channel.create, or
// for notify.channel.update when channel is set. Secrets are inputs only: a
// blank secret field keeps the stored value.
export function ChannelForm({ kind, channel, onCancel, onBack, onPlan }: { kind: ChannelKind; channel: NotifyChannel | null; onCancel: () => void; onBack?: () => void; onPlan: (input: Record<string, unknown>) => void }) {
  const groups = useQuery({ queryKey: GROUPS_QUERY_KEY, queryFn: listGroups })
  const editing = channel !== null
  const [name, setName] = useState(channel?.name ?? "")
  const [severity, setSeverity] = useState<Severity>(channel?.minSeverity ?? "warning")
  const [events, setEvents] = useState<Set<string>>(() => new Set(EVENT_GROUPS.filter((g) => g.kinds.some((k) => channel?.kinds.includes(k))).map((g) => g.id)))
  const [hostGroups, setHostGroups] = useState<Set<string>>(() => new Set(channel?.groupIds ?? []))
  const [enabled, setEnabled] = useState(channel?.enabled ?? true)
  const [f, setF] = useState<Record<string, string>>(() => ({
    server: channel?.ntfy?.server ?? "",
    chatId: channel?.telegram?.chatId ?? "",
    host: channel?.email?.host ?? "",
    port: String(channel?.email?.port ?? 587),
    security: channel?.email?.security ?? "starttls",
    username: channel?.email?.username ?? "",
    from: channel?.email?.from ?? "",
    recipients: channel?.email?.recipients.join("\n") ?? "",
  }))
  const [errors, setErrors] = useState<Errors>({})
  const set = (key: string) => (e: { target: { value: string } }) => setF((cur) => ({ ...cur, [key]: e.target.value }))
  const v = (key: string) => f[key] ?? ""
  const secretHint = (stored: boolean) => (editing && stored ? SAVED : undefined)

  function settings(found: Errors): Record<string, unknown> {
    const secret = (key: string, out: Record<string, unknown>, field: string, required: boolean, label: string) => {
      const value = v(key)
      if (value !== "") out[field] = value
      else if (required && !editing) found[key] = `Enter the ${label}.`
    }
    const out: Record<string, unknown> = {}
    switch (kind) {
      case "ntfy":
        if (v("server").trim() !== "") {
          if (urlWith(v("server").trim(), ["http:", "https:"]) === null) found.server = "Enter a URL that starts with http:// or https://."
          else out.server = v("server").trim()
        }
        if (v("topic") !== "" && !TOPIC.test(v("topic"))) found.topic = "A topic is 1 to 64 letters, digits, hyphens or underscores."
        secret("topic", out, "topic", true, "topic")
        secret("token", out, "token", false, "access token")
        break
      case "telegram":
        secret("botToken", out, "botToken", true, "bot token")
        if (!CHAT_ID.test(v("chatId").trim())) found.chatId = "Enter a chat id: a number, or @ and a channel's name."
        out.chatId = v("chatId").trim()
        break
      case "discord":
        if (v("webhookUrl") !== "") {
          const u = urlWith(v("webhookUrl"), ["https:"])
          if (u === null || !u.pathname.startsWith("/api/webhooks/")) found.webhookUrl = "Enter the webhook URL, https://discord.com/api/webhooks/…"
        }
        secret("webhookUrl", out, "webhookUrl", true, "webhook URL")
        break
      case "email": {
        if (v("host").trim() === "") found.host = "Enter the SMTP host."
        const port = Number(v("port"))
        if (!/^[0-9]+$/.test(v("port").trim()) || port < 1 || port > 65535) found.port = "Port must be 1 to 65535."
        if (v("from").trim() === "") found.from = "Enter the sender address."
        const recipients = v("recipients")
          .split(/[\s,;]+/)
          .filter((r) => r !== "")
        if (recipients.length < 1 || recipients.length > 10) found.recipients = "Enter 1 to 10 addresses."
        Object.assign(out, { host: v("host").trim(), port, security: v("security"), from: v("from").trim(), recipients })
        if (v("username").trim() !== "") out.username = v("username").trim()
        secret("password", out, "password", false, "password")
        break
      }
      case "webhook":
        if (v("url") !== "" && urlWith(v("url"), ["http:", "https:"]) === null) found.url = "Enter a URL that starts with http:// or https://."
        secret("url", out, "url", true, "URL")
        secret("secret", out, "secret", false, "signing secret")
        break
    }
    return out
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const found: Errors = {}
    if (!NAME.test(name.trim())) found.name = "A name is 1 to 48 letters, digits, spaces, dots, underscores or hyphens."
    const kindSettings = settings(found)
    setErrors(found)
    if (Object.keys(found).length > 0) return
    const kinds = EVENT_GROUPS.filter((g) => events.has(g.id)).flatMap((g) => g.kinds)
    const common = { name: name.trim(), minSeverity: severity, kinds, groupIds: [...hostGroups].sort(), [kind]: kindSettings }
    onPlan(editing ? { id: channel.id, ...common, enabled } : { kind, ...common })
  }

  const label = KIND_LABELS[kind]
  const title = editing ? `Edit ${channel.name}` : `Add ${kind === "email" ? "an email" : `a ${label}`} channel`
  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onCancel())}>
      <DialogContent className="max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{editing ? `A ${label} channel.` : "Secrets are write-only: they are never shown again."}</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" noValidate onSubmit={submit}>
          <Field id="ch-name" label="Name" error={errors.name}>
            <Input {...note("ch-name", errors, "name", false)} value={name} autoComplete="off" autoFocus maxLength={48} onChange={(e) => setName(e.target.value)} />
          </Field>
          {kind === "ntfy" && (
            <>
              <Field id="ch-server" label="Server URL" hint="https://ntfy.sh when empty." error={errors.server}>
                <Input {...note("ch-server", errors, "server", true)} value={v("server")} autoComplete="off" placeholder="https://ntfy.sh" onChange={set("server")} />
              </Field>
              <Field id="ch-topic" label="Topic" hint={secretHint(true)} error={errors.topic}>
                <Input {...note("ch-topic", errors, "topic", editing)} type="password" autoComplete="new-password" value={v("topic")} onChange={set("topic")} />
              </Field>
              <Field id="ch-token" label="Access token (optional)" hint={secretHint(channel?.ntfy?.tokenSet === true)}>
                <Input {...note("ch-token", errors, "token", editing && channel.ntfy?.tokenSet === true)} type="password" autoComplete="new-password" value={v("token")} onChange={set("token")} />
              </Field>
            </>
          )}
          {kind === "telegram" && (
            <>
              <Field id="ch-bot" label="Bot token" hint={secretHint(true)} error={errors.botToken}>
                <Input {...note("ch-bot", errors, "botToken", editing)} type="password" autoComplete="new-password" value={v("botToken")} onChange={set("botToken")} />
              </Field>
              <Field id="ch-chat" label="Chat ID" error={errors.chatId}>
                <Input {...note("ch-chat", errors, "chatId", false)} value={v("chatId")} autoComplete="off" placeholder="-1001234567890" onChange={set("chatId")} />
              </Field>
            </>
          )}
          {kind === "discord" && (
            <Field id="ch-discord" label="Webhook URL" hint={secretHint(true)} error={errors.webhookUrl}>
              <Input {...note("ch-discord", errors, "webhookUrl", editing)} type="password" autoComplete="new-password" value={v("webhookUrl")} onChange={set("webhookUrl")} />
            </Field>
          )}
          {kind === "email" && (
            <>
              <Field id="ch-host" label="SMTP host" error={errors.host}>
                <Input {...note("ch-host", errors, "host", false)} value={v("host")} autoComplete="off" placeholder="smtp.example.com" onChange={set("host")} />
              </Field>
              <Field id="ch-port" label="Port" error={errors.port}>
                <Input {...note("ch-port", errors, "port", false)} value={v("port")} inputMode="numeric" autoComplete="off" onChange={set("port")} />
              </Field>
              <div className="flex flex-col gap-1.5">
                <span id="ch-security" className="text-sm font-medium">
                  Security
                </span>
                <SegmentGroup value={v("security")} onValueChange={(s) => setF((cur) => ({ ...cur, security: s }))} aria-labelledby="ch-security" variant="outline">
                  <SegmentGroupIndicator />
                  <SegmentGroupItem value="starttls">STARTTLS</SegmentGroupItem>
                  <SegmentGroupItem value="tls">TLS</SegmentGroupItem>
                  <SegmentGroupItem value="none">None</SegmentGroupItem>
                </SegmentGroup>
              </div>
              <Field id="ch-user" label="Username (optional)">
                <Input id="ch-user" value={v("username")} autoComplete="off" onChange={set("username")} />
              </Field>
              <Field id="ch-password" label="Password (optional)" hint={secretHint(channel?.email?.passwordSet === true)}>
                <Input {...note("ch-password", errors, "password", editing && channel.email?.passwordSet === true)} type="password" autoComplete="new-password" value={v("password")} onChange={set("password")} />
              </Field>
              <Field id="ch-from" label="From" error={errors.from}>
                <Input {...note("ch-from", errors, "from", false)} type="email" value={v("from")} autoComplete="off" placeholder="darkpanel@example.com" onChange={set("from")} />
              </Field>
              <Field id="ch-to" label="Recipients" hint="1 to 10 addresses, one per line or separated by commas." error={errors.recipients}>
                <Textarea {...note("ch-to", errors, "recipients", true)} rows={3} value={v("recipients")} onChange={set("recipients")} />
              </Field>
            </>
          )}
          {kind === "webhook" && (
            <>
              <Field id="ch-url" label="URL" hint={secretHint(true)} error={errors.url}>
                <Input {...note("ch-url", errors, "url", editing)} type="password" autoComplete="new-password" value={v("url")} onChange={set("url")} />
              </Field>
              <Field id="ch-secret" label="Signing secret (optional)" hint={secretHint(channel?.webhook?.secretSet === true)}>
                <Input {...note("ch-secret", errors, "secret", editing && channel.webhook?.secretSet === true)} type="password" autoComplete="new-password" value={v("secret")} onChange={set("secret")} />
              </Field>
            </>
          )}
          <div className="flex flex-col gap-1.5">
            <span id="ch-severity" className="text-sm font-medium">
              Lowest severity
            </span>
            <SegmentGroup value={severity} onValueChange={(s) => setSeverity(s as Severity)} aria-labelledby="ch-severity" variant="outline">
              <SegmentGroupIndicator />
              <SegmentGroupItem value="info">Info</SegmentGroupItem>
              <SegmentGroupItem value="warning">Warning</SegmentGroupItem>
              <SegmentGroupItem value="critical">Critical</SegmentGroupItem>
            </SegmentGroup>
          </div>
          <Ticks legend="Events" hint="None ticked: every event." items={EVENT_GROUPS} value={events} onChange={setEvents} />
          {groups.data !== undefined && groups.data.groups.length > 0 && (
            <Ticks legend="Host groups" hint="None ticked: every host." items={groups.data.groups.map((g) => ({ id: g.id, label: g.name }))} value={hostGroups} onChange={setHostGroups} />
          )}
          {editing && (
            <div className="flex items-center justify-between gap-2">
              <Label htmlFor="ch-enabled">Receives events</Label>
              <Switch id="ch-enabled" checked={enabled} onCheckedChange={setEnabled} />
            </div>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={onBack ?? onCancel}>
              {onBack !== undefined ? "Back" : "Cancel"}
            </Button>
            <Button type="submit">{editing ? "Review change" : "Review channel"}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
