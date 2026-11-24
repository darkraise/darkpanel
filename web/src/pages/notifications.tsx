import { useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Bell, CircleCheck, CircleX, Mail, MessageCircle, MoreHorizontal, Send, Webhook, type LucideIcon } from "lucide-react"
import { PageHeader } from "darkraise-ui/layout"
import { Alert, AlertDescription } from "darkraise-ui/components/alert"
import { Button } from "darkraise-ui/components/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "darkraise-ui/components/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "darkraise-ui/components/dialog"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "darkraise-ui/components/dropdown-menu"
import { EmptyState } from "darkraise-ui/components/empty-state"
import { Skeleton } from "darkraise-ui/components/skeleton"
import { GROUPS_QUERY_KEY, errorMessage, listGroups, readAction } from "@/api/client"
import type { ChannelKind, ChannelList, ChannelTestResult, HostGroup, NotifyChannel } from "@/api/types"
import { ChangeDialog } from "@/components/change-dialog"
import { formatTime } from "@/lib/format"
import { CHANNELS_QUERY_KEY, CHANNEL_KINDS, KIND_LABELS, lastDeliveryText, routingText } from "@/lib/notify"
import { usePageTitle } from "@/lib/page-title"
import { applied } from "@/lib/users"
import { ChannelForm } from "./channel-form"

const ICONS: Record<ChannelKind, LucideIcon> = { ntfy: Bell, telegram: Send, discord: MessageCircle, email: Mail, webhook: Webhook }

type Dialogue =
  | { kind: "pick" }
  | { kind: "form"; channelKind: ChannelKind; channel: NotifyChannel | null }
  | { kind: "change"; title: string; name: string; input: Record<string, unknown> }

type TestState = { state: "pending" } | { state: "done"; text: string; ok: boolean }

function listChannels(): Promise<ChannelList> {
  return readAction<ChannelList>("notify.channel.list", 1)
}

function ChannelCard({ channel, groups, onPick }: { channel: NotifyChannel; groups: HostGroup[] | undefined; onPick: (d: Dialogue) => void }) {
  const client = useQueryClient()
  const [test, setTest] = useState<TestState | null>(null)
  const Icon = ICONS[channel.kind]
  const last = channel.lastDelivery
  const pending = test?.state === "pending"

  async function sendTest() {
    setTest({ state: "pending" })
    try {
      const res = await readAction<ChannelTestResult>("notify.channel.test", 1, { id: channel.id })
      setTest(res.delivered ? { state: "done", text: "Delivered", ok: true } : { state: "done", text: `Failed: ${res.code}`, ok: false })
    } catch (err) {
      setTest({ state: "done", text: `Failed: ${errorMessage(err)}`, ok: false })
    }
    void client.invalidateQueries({ queryKey: CHANNELS_QUERY_KEY })
  }

  const LastIcon = last === null ? null : last.state === "sent" ? CircleCheck : CircleX
  return (
    <Card role="region" aria-labelledby={`channel-${channel.id}`}>
      <CardHeader className="flex flex-row items-center gap-3 space-y-0">
        <Icon aria-hidden className="size-5 shrink-0 text-muted-foreground" />
        <div className="flex min-w-0 flex-1 flex-col">
          <CardTitle id={`channel-${channel.id}`} className="wrap-anywhere">
            <h2>{channel.name}</h2>
          </CardTitle>
          <CardDescription>{channel.enabled ? KIND_LABELS[channel.kind] : `${KIND_LABELS[channel.kind]}, off`}</CardDescription>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" aria-label={`Actions for ${channel.name}`}>
              <MoreHorizontal aria-hidden />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => onPick({ kind: "form", channelKind: channel.kind, channel })}>Edit</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => onPick({ kind: "change", title: `Delete ${channel.name}`, name: "notify.channel.delete", input: { id: channel.id } })}>
              Delete
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 text-sm">
        <p className="wrap-anywhere">{routingText(channel, groups)}</p>
        <p className="flex items-center gap-1.5 text-muted-foreground">
          {LastIcon !== null && !pending && <LastIcon aria-hidden className="size-4 shrink-0" />}
          <span className={last?.state === "failed" && !pending ? "font-medium text-foreground" : undefined}>
            {`Last delivery: ${pending ? "Sending test…" : lastDeliveryText(channel, formatTime)}`}
          </span>
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <Button variant="outline" size="sm" loading={pending} disabled={pending} onClick={() => void sendTest()}>
            {pending ? "Sending test…" : "Send test"}
          </Button>
          {/* While pending, the button and the delivery line show it; the live region only announces it. */}
          <span role="status" className={test?.state === "done" && !test.ok ? "font-medium" : "text-muted-foreground"}>
            {test === null ? "" : pending ? <span className="sr-only">Sending test…</span> : test.text}
          </span>
        </div>
      </CardContent>
    </Card>
  )
}

function KindPicker({ onCancel, onPick }: { onCancel: () => void; onPick: (kind: ChannelKind) => void }) {
  return (
    <Dialog open onOpenChange={(next) => (next ? undefined : onCancel())}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add channel</DialogTitle>
          <DialogDescription>Choose where notifications go.</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          {CHANNEL_KINDS.map((k) => {
            const Icon = ICONS[k]
            return (
              <Button key={k} variant="outline" className="justify-start" onClick={() => onPick(k)}>
                <Icon aria-hidden /> {KIND_LABELS[k]}
              </Button>
            )
          })}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function NotificationsPage() {
  usePageTitle("Notifications")
  const client = useQueryClient()
  const channels = useQuery({ queryKey: CHANNELS_QUERY_KEY, queryFn: listChannels })
  const groups = useQuery({ queryKey: GROUPS_QUERY_KEY, queryFn: listGroups })
  const [dialogue, setDialogue] = useState<Dialogue | null>(null)
  const close = () => setDialogue(null)

  let body
  if (channels.isPending) {
    body = (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3" aria-label="Loading the channels">
        <Skeleton className="h-40 w-full" />
        <Skeleton className="h-40 w-full" />
      </div>
    )
  } else if (channels.isError) {
    body = (
      <Alert variant="destructive" role="alert">
        <AlertDescription className="flex flex-col items-start gap-2">
          <span>{`Couldn't load notification channels: ${errorMessage(channels.error)}`}</span>
          <Button size="sm" variant="outline" onClick={() => void channels.refetch()}>
            Retry
          </Button>
        </AlertDescription>
      </Alert>
    )
  } else if (channels.data.channels.length === 0) {
    // darkraise-ui's EmptyState title is an h3; the hidden h2 keeps the
    // heading levels from skipping one.
    body = (
      <>
        <h2 className="sr-only">Channel list</h2>
        <EmptyState icon={<Bell aria-hidden />} title="No channels yet" description="Add one to get notified of alerts and failed changes." />
      </>
    )
  } else {
    body = (
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        {channels.data.channels.map((c) => (
          <ChannelCard key={c.id} channel={c} groups={groups.data?.groups} onPick={setDialogue} />
        ))}
      </div>
    )
  }

  return (
    <>
      <PageHeader
        title="Notifications"
        description="Where Darkpanel sends alerts, failed changes and failed updates."
        actions={<Button onClick={() => setDialogue({ kind: "pick" })}>Add channel</Button>}
      />
      {body}
      {dialogue?.kind === "pick" && <KindPicker onCancel={close} onPick={(k) => setDialogue({ kind: "form", channelKind: k, channel: null })} />}
      {dialogue?.kind === "form" && (
        <ChannelForm
          kind={dialogue.channelKind}
          channel={dialogue.channel}
          onCancel={close}
          onBack={dialogue.channel === null ? () => setDialogue({ kind: "pick" }) : undefined}
          onPlan={(input) =>
            setDialogue(
              dialogue.channel === null
                ? { kind: "change", title: `Add the channel ${String(input.name)}`, name: "notify.channel.create", input }
                : { kind: "change", title: `Edit ${dialogue.channel.name}`, name: "notify.channel.update", input },
            )
          }
        />
      )}
      {dialogue?.kind === "change" && (
        <ChangeDialog
          open
          onOpenChange={(next) => (next ? undefined : close())}
          title={dialogue.title}
          target={{ name: dialogue.name, version: 1 }}
          input={dialogue.input}
          onApplied={(change) => {
            if (applied(change)) void client.invalidateQueries({ queryKey: CHANNELS_QUERY_KEY })
          }}
        />
      )}
    </>
  )
}
